package pair

import(
    "github.com/brutella/hap"
    "io"
    "fmt"
    "encoding/hex"
    "bytes"
)

type VerifyClientController struct {
    PairingHandler
    context *hap.Context
    bridge *hap.Bridge
    session *VerifySession
    username string
    LTPK []byte
    LTSK []byte
}

func NewVerifyClientController(context *hap.Context, bridge *hap.Bridge, username string) *VerifyClientController {    
    LTPK, LTSK, _ := hap.ED25519GenerateKey(username)
        
    controller := VerifyClientController{
                                    username: username,
                                    context: context,
                                    bridge: bridge,
                                    session: NewVerifySession(),
                                    LTPK: LTPK,
                                    LTSK: LTSK,
                                }
    
    return &controller
}
    
func (c *VerifyClientController) Handle(cont_in Container) (Container, error) {
    var cont_out Container
    var err error
    
    method := cont_in.GetByte(TLVType_Method)
    
    // It is valid that method is not sent
    // If method is sent then it must be 0x00
    if method != 0x00 {
        return nil, hap.NewErrorf("Cannot handle auth method %b", method)
    }
    
    seq := cont_in.GetByte(TLVType_SequenceNumber)
    switch seq {
    case VerifyStartRespond:
        cont_out, err = c.handlePairVerifyRespond(cont_in)
    case VerifyFinishRespond:        
        cont_out, err = c.handlePairVerifyFinishRespond(cont_in)
    default:
        return nil, hap.NewErrorf("Cannot handle sequence number %d", seq)
    }
    
    return cont_out, err
}

// Client -> Server
// - Public key `A`
func (c *VerifyClientController) InitialKeyVerifyRequest() (io.Reader) {
    cont_out := NewTLV8Container()
    cont_out.SetByte(TLVType_Method, 0)
    cont_out.SetByte(TLVType_SequenceNumber, VerifyStartRequest)
    cont_out.SetBytes(TLVType_PublicKey, c.session.publicKey[:])
    
    fmt.Println("<-     A:", hex.EncodeToString(cont_out.GetBytes(TLVType_PublicKey)))
    
    return cont_out.BytesBuffer()
}

// Server -> Client
// - B: server public key
// - encrypted message
//      - username
//      - signature: from server session public key, server name, client session public key
//
// Client -> Server
// - encrypted message
//      - username
//      - signature: from client session public key, server name, server session public key,
func (c *VerifyClientController) handlePairVerifyRespond(cont_in Container) (Container, error) {        
    serverPublicKey := cont_in.GetBytes(TLVType_PublicKey)
    if len(serverPublicKey) != 32 {
        return nil, hap.NewErrorf("Invalid server public key size %d", len(serverPublicKey))
    }
    
    var otherPublicKey [32]byte
    copy(otherPublicKey[:], serverPublicKey)
    c.session.GenerateSharedKeyWithOtherPublicKey(otherPublicKey)
    c.session.SetupEncryptionKey([]byte("Pair-Verify-Encrypt-Salt"), []byte("Pair-Verify-Encrypt-Info"))
    
    fmt.Println("Client")
    fmt.Println("->   B:", hex.EncodeToString(serverPublicKey))
    fmt.Println("     S:", hex.EncodeToString(c.session.secretKey[:]))
    fmt.Println("Shared:", hex.EncodeToString(c.session.sharedKey[:]))
    fmt.Println("     K:", hex.EncodeToString(c.session.encryptionKey[:]))
    
    // Decrypt
    data := cont_in.GetBytes(TLVType_EncryptedData)
    message := data[:(len(data) - 16)]
    var mac [16]byte
    copy(mac[:], data[len(message):]) // 16 byte (MAC)    
    
    decrypted, err := hap.Chacha20DecryptAndPoly1305Verify(c.session.encryptionKey[:], []byte("PV-Msg02"), message, mac, nil)
    if err != nil {
        return nil, err
    }
    
    decrypted_buffer := bytes.NewBuffer(decrypted)
    tlv_decrypted, err := NewTLV8ContainerFromReader(decrypted_buffer)
    if err != nil {
        return nil, err
    }
    
    username  := tlv_decrypted.GetString(TLVType_Username)
    signature := tlv_decrypted.GetBytes(TLVType_Ed25519Signature)
    
    fmt.Println("    Username:", username)
    fmt.Println("   Signature:", hex.EncodeToString(signature))
    
    // Validate signature
    material := make([]byte, 0)
    material = append(material, c.session.otherPublicKey[:]...)
    material = append(material, username...)
    material = append(material, c.session.publicKey[:]...)
    
    LTPK := c.context.PublicKeyForAccessory(c.bridge)
    
    if hap.ValidateED25519Signature(LTPK, material, signature) == false {
        return nil, hap.NewErrorf("Could not validate signature")
    }
    
    cont_out := NewTLV8Container()
    cont_out.SetByte(TLVType_Method, 0)
    cont_out.SetByte(TLVType_SequenceNumber, VerifyFinishRequest)
    
    tlv_encrypt := NewTLV8Container()
    tlv_encrypt.SetString(TLVType_Username, c.username)
    
    material = make([]byte, 0)
    material = append(material, c.session.publicKey[:]...)
    material = append(material, c.username...)
    material = append(material, c.session.otherPublicKey[:]...)
    
    signature, err = hap.ED25519Signature(c.LTSK, material)
    if err != nil {
        return nil, err
    }
    
    tlv_encrypt.SetBytes(TLVType_Ed25519Signature, signature)
    
    encrypted, mac, _ := hap.Chacha20EncryptAndPoly1305Seal(c.session.encryptionKey[:], []byte("PV-Msg03"), tlv_encrypt.BytesBuffer().Bytes(), nil)
    
    cont_out.SetBytes(TLVType_EncryptedData, append(encrypted, mac[:]...))
    
    return cont_out, nil
}

// Server -> Client
// - only error ocde (optional)
func (c *VerifyClientController) handlePairVerifyFinishRespond(cont_in Container) (Container, error) {    
    err_code := cont_in.GetByte(TLVStatus_NoError)
    if err_code != 0x00 {
        fmt.Println("Unexpected error %d", err_code)
    }
    
    return nil, nil
}