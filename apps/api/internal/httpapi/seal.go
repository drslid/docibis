package httpapi

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/digitorus/pdf"
	pdfsign "github.com/digitorus/pdfsign/sign"
)

// The electronic seal of the PDFs that ask for it (seal=true): a PAdES
// signature (ETSI.CAdES.detached, SHA-256) with the certificate of the
// organization, which certifies the document (no change allowed but filling
// forms). SEAL_CERT holds the certificate and its chain, SEAL_KEY its private
// key, both in PEM; read at start, the key stays in memory only. A PKCS#12
// file converts with: openssl pkcs12 -in seal.p12 -nodes.

type sealer struct {
	key   crypto.Signer
	chain []*x509.Certificate
}

func loadSealer() (*sealer, error) {
	certs, keyPEM := os.Getenv("SEAL_CERT"), os.Getenv("SEAL_KEY")
	if certs == "" && keyPEM == "" {
		return nil, nil
	}
	var s sealer
	for rest := []byte(certs); ; {
		var block *pem.Block
		if block, rest = pem.Decode(rest); block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("SEAL_CERT: %w", err)
		}
		s.chain = append(s.chain, c)
	}
	if len(s.chain) == 0 {
		return nil, errors.New("SEAL_CERT: no PEM certificate")
	}
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, errors.New("SEAL_KEY: no PEM private key")
	}
	var key any
	var err error
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	}
	if err != nil {
		return nil, fmt.Errorf("SEAL_KEY: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, errors.New("SEAL_KEY: unsupported key")
	}
	type publicKey interface{ Equal(crypto.PublicKey) bool }
	if pub, ok := signer.Public().(publicKey); !ok || !pub.Equal(s.chain[0].PublicKey) {
		return nil, errors.New("SEAL_KEY does not match the first certificate of SEAL_CERT")
	}
	switch signer.(type) {
	case *rsa.PrivateKey, *ecdsa.PrivateKey, ed25519.PrivateKey:
	default:
		return nil, errors.New("SEAL_KEY: RSA, ECDSA or Ed25519 expected")
	}
	s.key = signer
	return &s, nil
}

// seal certifies a PDF with the seal of the organization.
func (s *sealer) seal(raw []byte) ([]byte, error) {
	rdr, err := pdf.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	cert := s.chain[0]
	var out bytes.Buffer
	err = pdfsign.Sign(bytes.NewReader(raw), &out, rdr, int64(len(raw)), pdfsign.SignData{
		Signature: pdfsign.SignDataSignature{
			CertType:   pdfsign.CertificationSignature,
			DocMDPPerm: pdfsign.AllowFillingExistingFormFieldsAndSignaturesPerms,
			Info: pdfsign.SignDataSignatureInfo{
				Name:   strings.TrimSpace(cert.Subject.CommonName),
				Reason: "Cachet électronique",
				Date:   time.Now(),
			},
		},
		Signer:            s.key,
		DigestAlgorithm:   crypto.SHA256,
		Certificate:       cert,
		CertificateChains: [][]*x509.Certificate{s.chain},
		SubFilter:         pdfsign.SubFilterETSICAdESDetached,
	})
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// sealDocument applies the seal a request asks for.
func (s *Server) sealDocument(doc document) ([]byte, error) {
	switch {
	case doc.format != "pdf":
		return nil, refuse("Le cachet électronique (seal) s'applique aux PDF.")
	case s.sealer == nil:
		return nil, refuse("Le cachet électronique n'est pas configuré sur ce serveur (variables SEAL_CERT et SEAL_KEY).")
	}
	out, err := s.sealer.seal(doc.content)
	if err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	return out, nil
}
