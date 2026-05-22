package audio

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"io"
)

type Decryptor struct {
	reader io.ReaderAt
	cipher cipher.Block
}

var baseIv = []byte{0x72, 0xe0, 0x67, 0xfb, 0xdd, 0xcb, 0xcf, 0x77, 0xeb, 0xe8, 0xbc, 0x64, 0x3f, 0x63, 0x0d, 0x93}

func NewAesAudioDecryptor(r io.ReaderAt, key []byte) (*Decryptor, error) {
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	return &Decryptor{r, c}, nil
}

func (a *Decryptor) ReadAt(p []byte, pos int64) (n int, err error) {
	bs := int64(a.cipher.BlockSize())
	block, off := uint64(pos/bs), int(pos%bs)

	counter := binary.BigEndian.Uint64(baseIv[8:])
	counter += block
	newIv := make([]byte, len(baseIv))
	copy(newIv, baseIv)
	binary.BigEndian.PutUint64(newIv[8:], counter)

	stream := cipher.NewCTR(a.cipher, newIv)

	if off > 0 {
		discard := make([]byte, off)
		stream.XORKeyStream(discard, discard)
	}

	n, err = a.reader.ReadAt(p, pos)
	if n > 0 {
		stream.XORKeyStream(p[:n], p[:n])
	}
	return n, err
}

func (a *Decryptor) Close() error {
	if closer, ok := a.reader.(io.Closer); ok {
		return closer.Close()
	}

	return nil
}
