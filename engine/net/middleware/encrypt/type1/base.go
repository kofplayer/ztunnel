package type1NetEncrypt

import (
	cryptoRand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"fmt"
	"math/rand"
	netMiddleware "ztunnel/engine/net/middleware"
)

var (
	enTable = [256]uint8{182, 156, 28, 39, 102, 215, 66, 205, 193, 254, 82, 57, 253, 5, 92, 54, 147, 24, 56, 186, 19, 181, 26, 59, 220, 58, 4, 69, 204, 15, 178, 51, 242, 217, 218, 247, 196, 169, 121, 170, 202, 84, 241, 31, 159, 212, 7, 9, 130, 198, 43, 71, 237, 48, 53, 146, 113, 128, 20, 93, 17, 29, 222, 239, 119, 236, 213, 110, 165, 94, 192, 114, 151, 195, 6, 226, 111, 25, 118, 152, 2, 47, 88, 105, 255, 131, 230, 78, 1, 194, 232, 108, 142, 148, 184, 75, 98, 162, 49, 233, 38, 167, 190, 251, 201, 141, 44, 11, 65, 8, 3, 227, 50, 103, 99, 214, 248, 150, 63, 179, 139, 91, 83, 231, 52, 21, 74, 154, 175, 12, 45, 140, 136, 252, 124, 107, 224, 73, 250, 191, 145, 249, 64, 72, 77, 23, 185, 189, 55, 126, 112, 85, 87, 70, 177, 135, 149, 18, 106, 41, 81, 209, 137, 166, 211, 206, 197, 188, 100, 138, 90, 132, 89, 157, 133, 221, 16, 200, 158, 216, 207, 127, 115, 68, 225, 101, 67, 30, 36, 176, 229, 120, 0, 174, 60, 183, 161, 40, 246, 235, 96, 245, 172, 42, 143, 32, 76, 180, 134, 123, 14, 117, 109, 240, 210, 144, 10, 208, 122, 163, 168, 80, 153, 164, 62, 22, 37, 129, 27, 219, 35, 244, 160, 95, 46, 171, 61, 13, 223, 228, 97, 199, 116, 238, 125, 243, 86, 173, 234, 155, 34, 33, 104, 203, 79, 187}
	deTable = [256]uint8{192, 88, 80, 110, 26, 13, 74, 46, 109, 47, 216, 107, 129, 237, 210, 29, 176, 60, 157, 20, 58, 125, 225, 145, 17, 77, 22, 228, 2, 61, 187, 43, 205, 251, 250, 230, 188, 226, 100, 3, 197, 159, 203, 50, 106, 130, 234, 81, 53, 98, 112, 31, 124, 54, 15, 148, 18, 11, 25, 23, 194, 236, 224, 118, 142, 108, 6, 186, 183, 27, 153, 51, 143, 137, 126, 95, 206, 144, 87, 254, 221, 160, 10, 122, 41, 151, 246, 152, 82, 172, 170, 121, 14, 59, 69, 233, 200, 240, 96, 114, 168, 185, 4, 113, 252, 83, 158, 135, 91, 212, 67, 76, 150, 56, 71, 182, 242, 211, 78, 64, 191, 38, 218, 209, 134, 244, 149, 181, 57, 227, 48, 85, 171, 174, 208, 155, 132, 162, 169, 120, 131, 105, 92, 204, 215, 140, 55, 16, 93, 156, 117, 72, 79, 222, 127, 249, 1, 173, 178, 44, 232, 196, 97, 219, 223, 68, 163, 101, 220, 37, 39, 235, 202, 247, 193, 128, 189, 154, 30, 119, 207, 21, 0, 195, 94, 146, 19, 255, 167, 147, 102, 139, 70, 8, 89, 73, 36, 166, 49, 241, 177, 104, 40, 253, 28, 7, 165, 180, 217, 161, 214, 164, 45, 66, 115, 5, 179, 33, 34, 229, 24, 175, 62, 238, 136, 184, 75, 111, 239, 190, 86, 123, 90, 99, 248, 199, 65, 52, 243, 63, 213, 42, 32, 245, 231, 201, 198, 35, 116, 141, 138, 103, 133, 12, 9, 84}
)

type Key uint64

const keySize = 8

type BaseNetEncrypt struct {
	netMiddleware.MiddlewareBase
	Key1 Key
	Key2 Key
	CsNo Key
	ScNo Key
}

func (m *BaseNetEncrypt) GenKey() Key {
	return Key(rand.Uint64())
}

func (m *BaseNetEncrypt) GetKeyBytes(key Key, size int) []byte {
	r := make([]byte, 0, size)
	for i := range size {
		s := (byte)((key >> (i * 8)) & 0xFF)
		r = append(r, s)
	}
	return r
}

func (m *BaseNetEncrypt) GetKeyByBytes(data []byte, size int) Key {
	var key Key = 0
	if size > len(data) {
		size = len(data)
	}
	for i := range size {
		c := data[i]
		key |= Key(c) << (i * 8)
	}
	return key
}

func (m *BaseNetEncrypt) TableEncrypt(data []byte) {
	for i := range data {
		data[i] = enTable[data[i]]
	}
}

func (m *BaseNetEncrypt) TableDecrypt(data []byte) {
	for i := range data {
		data[i] = deTable[data[i]]
	}
}

func (m *BaseNetEncrypt) getKeyAndStrBytes(key Key, data []byte) []byte {
	moveBit := (keySize / 2) * 8
	key1 := key >> moveBit
	key2 := key & (((Key(1)) << moveBit) - 1)
	data1 := m.GetKeyBytes(key1, keySize/2)
	data2 := m.GetKeyBytes(key2, keySize/2)
	data1 = append(data1, data...)
	data1 = append(data1, data2...)
	return data1
}

func (m *BaseNetEncrypt) getKeyAndStrByBytes(data []byte) (Key, []byte) {
	strlen := len(data) - keySize
	if strlen < 0 {
		return 0, nil
	}
	key1 := m.GetKeyByBytes(data, keySize/2)
	str := data[keySize/2 : keySize/2+strlen]
	key2 := m.GetKeyByBytes(data[keySize/2+strlen:], keySize/2)
	key := (key1 << ((keySize / 2) * 8)) | key2
	return key, str
}

func (m *BaseNetEncrypt) GetPublicKeyByBytes(data []byte) (*rsa.PublicKey, error) {
	pubInterface, err := x509.ParsePKIXPublicKey(data)
	if err != nil {
		return nil, err
	}
	pub, ok := pubInterface.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("failed to decode public key")
	}
	return pub, nil
}

func (m *BaseNetEncrypt) EncryptWithPublicKey(data []byte, publicKey *rsa.PublicKey) ([]byte, error) {
	encrypted, err := rsa.EncryptOAEP(sha256.New(), cryptoRand.Reader, publicKey, data, nil)
	if err != nil {
		return nil, err
	}
	return encrypted, nil
}

func (m *BaseNetEncrypt) DecryptWithPrivateKey(encryptedData []byte, privateKey *rsa.PrivateKey) ([]byte, error) {
	decrypted, err := rsa.DecryptOAEP(sha256.New(), cryptoRand.Reader, privateKey, encryptedData, nil)
	if err != nil {
		return nil, err
	}
	return decrypted, nil
}

func (m *BaseNetEncrypt) GetType1NetEncryptKey(keys ...Key) Key {
	var key Key
	for i, k := range keys {
		if i == 0 {
			key = k
		} else {
			key ^= k
		}
	}
	return key
}

func (m *BaseNetEncrypt) KeyMaskData(data []byte, keys ...Key) {
	key := m.GetType1NetEncryptKey(keys...)
	var masks [keySize]uint8
	for i := range masks {
		masks[i] = uint8((key >> i) & 0xFF)
	}
	for i := range data {
		data[i] ^= masks[i%keySize]
	}
}

func (m *BaseNetEncrypt) Key1Encrypt(data []byte) {
	m.KeyMaskData(data, m.Key1)
}

func (m *BaseNetEncrypt) Key1Decrypt(data []byte) {
	m.KeyMaskData(data, m.Key1)
}

func (m *BaseNetEncrypt) Key1Key2CsNoEncrypt(data []byte) {
	m.KeyMaskData(data, m.Key1, m.Key2, m.CsNo)
}

func (m *BaseNetEncrypt) Key1Key2CsNoDecrypt(data []byte) {
	m.KeyMaskData(data, m.Key1, m.Key2, m.CsNo)
}

func (m *BaseNetEncrypt) Key1Key2ScNoEncrypt(data []byte) {
	m.KeyMaskData(data, m.Key1, m.Key2, m.ScNo)
}

func (m *BaseNetEncrypt) Key1Key2ScNoDecrypt(data []byte) {
	m.KeyMaskData(data, m.Key1, m.Key2, m.ScNo)
}

func (m *BaseNetEncrypt) GoNextCsNo() {
	m.CsNo += (m.Key1 - m.Key2) ^ m.CsNo
}

func (m *BaseNetEncrypt) GoNextScNo() {
	m.ScNo += (m.Key2 - m.Key1) ^ m.ScNo
}
