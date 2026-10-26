package staff

import (
	"bytes"
	"crypto"
	_ "crypto/sha256"
	"crypto/x509"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/digitorus/pdf"
	"github.com/digitorus/pdfsign/sign"
	"software.sslmate.com/src/go-pkcs12"
)

// ErrNoSigningCertificate means this reviewer has no .p12 stored on the server.
var ErrNoSigningCertificate = fmt.Errorf("the reviewer has no signing certificate on this server")

// SignPDF seals a health-certificate PDF with that staff member's stored .p12.
func SignPDF(dir string, staffID int64, signerName string, pdfBytes []byte) ([]byte, error) {
	p12, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("staff-%d.p12", staffID)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoSigningCertificate
		}
		return nil, err
	}
	pass, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("staff-%d.pass", staffID)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoSigningCertificate
		}
		return nil, err
	}
	key, cert, caCerts, err := pkcs12.DecodeChain(p12, strings.TrimSpace(string(pass)))
	if err != nil {
		return nil, err
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("signing key cannot sign")
	}
	input := bytes.NewReader(pdfBytes)
	rdr, err := pdf.NewReader(input, int64(len(pdfBytes)))
	if err != nil {
		return nil, err
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	chain := append([]*x509.Certificate{cert}, caCerts...)
	var out bytes.Buffer
	err = sign.Sign(input, &out, rdr, int64(len(pdfBytes)), sign.SignData{
		Signer:            signer,
		DigestAlgorithm:   crypto.SHA256,
		Certificate:       cert,
		CertificateChains: [][]*x509.Certificate{chain},
		Signature: sign.SignDataSignature{
			CertType:   sign.CertificationSignature,
			DocMDPPerm: sign.DoNotAllowAnyChangesPerms,
			Info: sign.SignDataSignatureInfo{
				Name:   strings.TrimSpace(signerName),
				Reason: "Health certificate",
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
