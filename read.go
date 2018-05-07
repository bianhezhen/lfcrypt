package lfcrypt

import (
	"bytes"
	"crypto/cipher"
	"crypto/hmac"
	"encoding/binary"
	"encoding/json"
	"hash"
	"io"
)

type readStream struct {
	cipher  cipher.AEAD
	key     keyId
	counter uint32
	r       io.Reader
	w       io.Writer
	mac     hash.Hash
	buf4    [4]byte
	buf2    [2]byte
}

func (e *etmCryptor) Decrypt(r io.Reader, w io.Writer) error {
	er := readStream{
		cipher: e.c,
		r:      r,
		w:      w,
		mac:    e.newTrailerHMAC(),
	}

	err := er.readHeader()
	if err != nil {
		return err
	}

	if e.keyid != er.key.KeyID {
		return ErrNoMatchingKey
	}

	err = er.copy()
	if err != nil {
		return err
	}

	return nil
}

func (er *readStream) readHeader() error {
	header := make([]byte, len(headerStr)+4)
	n, err := io.ReadFull(er.r, header)
	if err != nil {
		return err
	}

	if bytes.Compare([]byte(headerStr), header[0:len(headerStr)]) != 0 {
		return ErrUnknownHeader
	}

	if n != len(header) {
		return ErrShortRead
	}

	cipherType := binary.BigEndian.Uint32(header[len(headerStr):len(header)])

	switch cipherType {
	case AEAD_AES_256_CBC_HMAC_SHA_512_ID:
		// TODO: other cipher types
		break
	default:
		return ErrUnknownCipher
	}

	if er.mac != nil {
		er.mac.Write(header)
	}

	n, err = io.ReadFull(er.r, er.buf2[:])
	if err != nil {
		return err
	}
	if n != len(er.buf2) {
		return ErrShortRead
	}

	if er.mac != nil {
		er.mac.Write(er.buf2[:])
	}

	keyIdLen := binary.BigEndian.Uint16(er.buf2[:])
	kbuf := make([]byte, keyIdLen)
	n, err = io.ReadFull(er.r, kbuf)
	if err != nil {
		return err
	}
	if n != len(kbuf) {
		return ErrShortRead
	}
	if er.mac != nil {
		er.mac.Write(kbuf)
	}

	err = json.Unmarshal(kbuf, &er.key)
	if err != nil {
		return err
	}
	return nil
}

func (er *readStream) readCounter() error {
	n, err := io.ReadFull(er.r, er.buf4[:])
	if err != nil {
		return err
	}
	if n != len(er.buf4) {
		return ErrShortRead
	}
	er.mac.Write(er.buf4[:])

	incounter := binary.BigEndian.Uint32(er.buf4[:])
	if incounter != er.counter {
		return ErrCounterMismatch
	}
	er.counter++
	return nil
}

func (er *readStream) readSealedDataLen() (uint16, error) {
	n, err := io.ReadFull(er.r, er.buf2[:])
	if err != nil {
		return 0, err
	}
	if n != len(er.buf2) {
		return 0, ErrShortRead
	}

	er.mac.Write(er.buf2[:])

	slen := binary.BigEndian.Uint16(er.buf2[:])
	return slen, nil
}

func (er *readStream) readSealedData(slen uint16) error {
	buf := make([]byte, slen)
	clearbuf := make([]byte, 0, slen)

	_, err := io.ReadFull(er.r, buf)

	if err != nil {
		return err
	}

	er.mac.Write(buf)

	clearbuf, err = er.cipher.Open(clearbuf, nil, buf, []byte{})

	if err != nil {
		return err
	}

	_, err = er.w.Write(clearbuf)
	if err != nil {
		return err
	}
	return nil
}

func (er *readStream) readTrailingMAC() error {
	messageMAC := make([]byte, er.mac.Size())
	_, err := io.ReadFull(er.r, messageMAC)
	if err != nil {
		return err
	}

	expectedMAC := er.mac.Sum(nil)
	rv := hmac.Equal(messageMAC, expectedMAC)
	if rv != true {
		return ErrTrailingHMACMismatch
	}
	return nil
}

func (er *readStream) copy() error {
	for {
		err := er.readCounter()
		if err != nil {
			return err
		}

		slen, err := er.readSealedDataLen()
		if err != nil {
			return err
		}

		if slen == 0 {
			// empty data block, EOF coming.
			break
		}

		err = er.readSealedData(slen)
		if err != nil {
			return err
		}
	}

	err := er.readTrailingMAC()
	if err != nil {
		return err
	}

	return nil
}

// Read a KeyID block from a Reader. Note that the KeyID data is not authenticated or validated.
func ReadKeyId(r io.Reader) (uint32, error) {
	er := readStream{
		r: r,
	}

	err := er.readHeader()
	if err != nil {
		return 0, err
	}

	return er.key.KeyID, nil
}