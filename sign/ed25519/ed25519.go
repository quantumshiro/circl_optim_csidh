package ed25519

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"io"
	"math/bits"
)

// Size is the length in bytes of Ed25519 keys.
const Size = 32

// PublicKey represents a public key of Ed25519.
type PublicKey []byte

// PrivateKey represents a private key of Ed25519.
type PrivateKey []byte

// KeyPair implements crypto.Signer (golang.org/pkg/crypto/#Signer) interface.
type KeyPair struct{ private, public [Size]byte }

// GetPrivate returns a copy of the private key.
func (k *KeyPair) GetPrivate() PrivateKey { return makeCopy(&k.private) }

// GetPublic returns the public key corresponding to the private key.
func (k *KeyPair) GetPublic() PublicKey { return makeCopy(&k.public) }

// Public returns a crypto.PublicKey corresponding to the private key.
func (k *KeyPair) Public() crypto.PublicKey { return k.GetPublic() }

// Sign signs the given message with priv.
// Ed25519 performs two passes over messages to be signed and therefore cannot
// handle pre-hashed messages. Thus opts.HashFunc() must return zero to
// indicate the message hasn't been hashed. This can be achieved by passing
// crypto.Hash(0) as the value for opts.
func (k *KeyPair) Sign(rand io.Reader, message []byte, opts crypto.SignerOpts) ([]byte, error) {
	if opts.HashFunc() != crypto.Hash(0) {
		return nil, errors.New("ed25519: cannot sign hashed message")
	}
	return Sign(k, message), nil
}

// GenerateKey generates a public/private key pair using entropy from rand.
// If rand is nil, crypto/rand.Reader will be used.
func GenerateKey(rnd io.Reader) (*KeyPair, error) {
	if rnd == nil {
		rnd = rand.Reader
	}
	private := make(PrivateKey, Size)
	if _, err := io.ReadFull(rnd, private); err != nil {
		return nil, err
	}
	return NewKeyFromSeed(private), nil
}

// NewKeyFromSeed generates a pair of Ed25519 signing keys given a
// previously-generated private key.
func NewKeyFromSeed(private PrivateKey) *KeyPair {
	if l := len(private); l != Size {
		panic("ed25519: bad private key length")
	}
	var P pointR1
	pk := new(KeyPair)
	k := sha512.Sum512(private)
	clamp(k[:])
	reduceModOrder(k[:Size], false)
	P.fixedMult(k[:Size])
	P.ToBytes(pk.public[:])
	copy(pk.private[:], private[:Size])
	return pk
}

// Sign returns the signature of a message using both the private and public
// keys of the signer.
func Sign(k *KeyPair, message []byte) []byte {
	h := sha512.Sum512(k.private[:])
	clamp(h[:])
	H := sha512.New()
	_, _ = H.Write(h[Size:])
	_, _ = H.Write(message)
	r := H.Sum(nil)
	reduceModOrder(r[:], true)

	var P pointR1
	P.fixedMult(r[:Size])
	signature := make([]byte, 2*Size)
	P.ToBytes(signature[:Size])

	H.Reset()
	_, _ = H.Write(signature[:Size])
	_, _ = H.Write(k.public[:])
	_, _ = H.Write(message)
	hRAM := H.Sum(nil)
	reduceModOrder(hRAM[:], true)
	calculateS(signature[Size:], r[:Size], hRAM[:Size], h[:Size])
	return signature
}

// Verify returns true if the signature is valid. Failure cases are invalid
// signature, or when the public key cannot be decoded.
func Verify(public PublicKey, message, sig []byte) bool {
	if l := len(public); l != Size {
		panic("ed25519: bad public key length")
	}
	if isLtOrder := isLessThan(sig[Size:], curve.order[:Size]); !isLtOrder {
		return false
	}
	var P pointR1
	if ok := P.FromBytes(public); !ok {
		return false
	}

	H := sha512.New()
	_, _ = H.Write(sig[:Size])
	_, _ = H.Write(public)
	_, _ = H.Write(message)
	hRAM := H.Sum(nil)
	reduceModOrder(hRAM[:], true)
	var Q pointR1
	P.neg()
	Q.doubleMult(&P, sig[Size:], hRAM[:Size])
	var enc [Size]byte
	Q.ToBytes(enc[:])
	return bytes.Equal(enc[:], sig[:Size])
}

func clamp(k []byte) {
	k[0] &= 248
	k[Size-1] = (k[Size-1] & 127) | 64
}

func makeCopy(in *[Size]byte) []byte {
	out := make([]byte, Size)
	copy(out, in[:])
	return out
}

// reduceModOrder calculates k = k mod order of the curve.
func reduceModOrder(k []byte, is512Bit bool) {
	var X [((2 * Size) * 8) / 64]uint64
	numWords := len(k) >> 3
	for i := 0; i < numWords; i++ {
		X[i] = binary.LittleEndian.Uint64(k[i*8 : (i+1)*8])
	}
	red512(&X, is512Bit)
	for i := 0; i < numWords; i++ {
		binary.LittleEndian.PutUint64(k[i*8:(i+1)*8], X[i])
	}
}

// red512 calculates x = x mod Order of the curve.
func red512(x *[8]uint64, full bool) {
	// Implementation of Algs.(14.47)+(14.52) of Handbook of Applied
	// Cryptography, by A. Menezes, P. van Oorschot, and S. Vanstone.
	const ell0 = uint64(0x5812631a5cf5d3ed)
	const ell1 = uint64(0x14def9dea2f79cd6)
	const ell160 = uint64(0x812631a5cf5d3ed0)
	const ell161 = uint64(0x4def9dea2f79cd65)
	const ell162 = uint64(0x0000000000000001)

	var c0, c1, c2, c3 uint64
	r0, r1, r2, r3, r4 := x[0], x[1], x[2], x[3], uint64(0)

	if full {
		q0, q1, q2, q3 := x[4], x[5], x[6], x[7]

		for i := 0; i < 3; i++ {
			h0, s0 := bits.Mul64(q0, ell160)
			h1, s1 := bits.Mul64(q1, ell160)
			h2, s2 := bits.Mul64(q2, ell160)
			h3, s3 := bits.Mul64(q3, ell160)

			s1, c0 = bits.Add64(h0, s1, 0)
			s2, c1 = bits.Add64(h1, s2, c0)
			s3, c2 = bits.Add64(h2, s3, c1)
			s4, _ := bits.Add64(h3, 0, c2)

			h0, l0 := bits.Mul64(q0, ell161)
			h1, l1 := bits.Mul64(q1, ell161)
			h2, l2 := bits.Mul64(q2, ell161)
			h3, l3 := bits.Mul64(q3, ell161)

			l1, c0 = bits.Add64(h0, l1, 0)
			l2, c1 = bits.Add64(h1, l2, c0)
			l3, c2 = bits.Add64(h2, l3, c1)
			l4, _ := bits.Add64(h3, 0, c2)

			s1, c0 = bits.Add64(s1, l0, 0)
			s2, c1 = bits.Add64(s2, l1, c0)
			s3, c2 = bits.Add64(s3, l2, c1)
			s4, c3 = bits.Add64(s4, l3, c2)
			s5, s6 := bits.Add64(l4, 0, c3)

			s2, c0 = bits.Add64(s2, q0, 0)
			s3, c1 = bits.Add64(s3, q1, c0)
			s4, c2 = bits.Add64(s4, q2, c1)
			s5, c3 = bits.Add64(s5, q3, c2)
			s6, s7 := bits.Add64(s6, 0, c3)

			q := q0 | q1 | q2 | q3
			m := -((q | -q) >> 63) // if q=0 then m=0...0 else m=1..1
			s0 &= m
			s1 &= m
			s2 &= m
			s3 &= m
			q0, q1, q2, q3 = s4, s5, s6, s7

			if (i+1)%2 == 0 {
				r0, c0 = bits.Add64(r0, s0, 0)
				r1, c1 = bits.Add64(r1, s1, c0)
				r2, c2 = bits.Add64(r2, s2, c1)
				r3, c3 = bits.Add64(r3, s3, c2)
				r4, _ = bits.Add64(r4, 0, c3)
			} else {
				r0, c0 = bits.Sub64(r0, s0, 0)
				r1, c1 = bits.Sub64(r1, s1, c0)
				r2, c2 = bits.Sub64(r2, s2, c1)
				r3, c3 = bits.Sub64(r3, s3, c2)
				r4, _ = bits.Sub64(r4, 0, c3)
			}
		}

		m := -(r4 >> 63)
		r0, c0 = bits.Add64(r0, m&ell160, 0)
		r1, c1 = bits.Add64(r1, m&ell161, c0)
		r2, c2 = bits.Add64(r2, m&ell162, c1)
		r3, c3 = bits.Add64(r3, 0, c2)
		r4, _ = bits.Add64(r4, m&1, c3)
		x[4], x[5], x[6], x[7] = 0, 0, 0, 0
	}

	q0 := (r4 << 4) | (r3 >> 60)
	r3 &= (uint64(1) << 60) - 1

	h0, s0 := bits.Mul64(ell0, q0)
	h1, s1 := bits.Mul64(ell1, q0)
	s1, c0 = bits.Add64(h0, s1, 0)
	s2, _ := bits.Add64(h1, 0, c0)

	r0, c0 = bits.Sub64(r0, s0, 0)
	r1, c1 = bits.Sub64(r1, s1, c0)
	r2, c2 = bits.Sub64(r2, s2, c1)
	r3, _ = bits.Sub64(r3, 0, c2)

	x[0], x[1], x[2], x[3] = r0, r1, r2, r3
}

// calculateS performs s = r+k*a mod Order of the curve
func calculateS(s, r, k, a []byte) {
	K := [4]uint64{
		binary.LittleEndian.Uint64(k[0*8 : 1*8]),
		binary.LittleEndian.Uint64(k[1*8 : 2*8]),
		binary.LittleEndian.Uint64(k[2*8 : 3*8]),
		binary.LittleEndian.Uint64(k[3*8 : 4*8]),
	}
	S := [8]uint64{
		binary.LittleEndian.Uint64(r[0*8 : 1*8]),
		binary.LittleEndian.Uint64(r[1*8 : 2*8]),
		binary.LittleEndian.Uint64(r[2*8 : 3*8]),
		binary.LittleEndian.Uint64(r[3*8 : 4*8]),
	}
	var c3 uint64
	for i := range K {
		ai := binary.LittleEndian.Uint64(a[i*8 : (i+1)*8])

		h0, l0 := bits.Mul64(K[0], ai)
		h1, l1 := bits.Mul64(K[1], ai)
		h2, l2 := bits.Mul64(K[2], ai)
		h3, l3 := bits.Mul64(K[3], ai)

		l1, c0 := bits.Add64(h0, l1, 0)
		l2, c1 := bits.Add64(h1, l2, c0)
		l3, c2 := bits.Add64(h2, l3, c1)
		l4, _ := bits.Add64(h3, 0, c2)

		S[i+0], c0 = bits.Add64(S[i+0], l0, 0)
		S[i+1], c1 = bits.Add64(S[i+1], l1, c0)
		S[i+2], c2 = bits.Add64(S[i+2], l2, c1)
		S[i+3], c3 = bits.Add64(S[i+3], l3, c2)
		S[i+4], _ = bits.Add64(S[i+4], l4, c3)
	}
	red512(&S, true)
	binary.LittleEndian.PutUint64(s[0*8:1*8], S[0])
	binary.LittleEndian.PutUint64(s[1*8:2*8], S[1])
	binary.LittleEndian.PutUint64(s[2*8:3*8], S[2])
	binary.LittleEndian.PutUint64(s[3*8:4*8], S[3])
}

// isLessThan returns true if 0 <= x < y, both slices must have the same length.
func isLessThan(x, y []byte) bool {
	i := Size - 1
	for i > 0 && x[i] == y[i] {
		i--
	}
	return x[i] < y[i]
}
