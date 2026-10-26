package staff

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"software.sslmate.com/src/go-pkcs12"

	"hse-medpost/internal/middleware"
)

func isSigningPosition(position string) bool {
	return middleware.IsDoctorOrCMO(position)
}

func (a *API) signingFile(staffID int64) string {
	return filepath.Join(a.SigningDir, fmt.Sprintf("staff-%d.p12", staffID))
}

func (a *API) hasSigningFile(staffID int64) bool {
	if a.SigningDir == "" {
		return false
	}
	info, err := os.Stat(a.signingFile(staffID))
	return err == nil && !info.IsDir()
}

func (a *API) CreateSigningCertificate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	hidden, err := a.isBuiltInAdminStaff(id)
	if err != nil || hidden {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var name, position string
	err = a.DB.QueryRow(`SELECT StaffName, COALESCE(Position,'') FROM MedpostStaffTable WHERE StaffID = ?`, id).Scan(&name, &position)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if !isSigningPosition(position) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "signing certificates are only for a doctor or the Chief Medical Officer"})
		return
	}
	if strings.TrimSpace(a.SigningDir) == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "signing folder is not configured"})
		return
	}
	password, err := createSigningCertificate(a.SigningDir, id, name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create signing certificate"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"file":     filepath.Base(a.signingFile(id)),
		"password": password,
	})
}

// DeleteSigningCertificate removes this person's .p12 and password file.
// The shared certificate authority stays, so other doctors can still sign.
func (a *API) DeleteSigningCertificate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	hidden, err := a.isBuiltInAdminStaff(id)
	if err != nil || hidden {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var position string
	err = a.DB.QueryRow(`SELECT COALESCE(Position,'') FROM MedpostStaffTable WHERE StaffID = ?`, id).Scan(&position)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if !isSigningPosition(position) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "signing certificates are only for a doctor or the Chief Medical Officer"})
		return
	}
	if strings.TrimSpace(a.SigningDir) == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "signing folder is not configured"})
		return
	}
	if err := removeSigningCertificate(a.SigningDir, id); err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no signing certificate"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not delete signing certificate"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func removeSigningCertificate(dir string, staffID int64) error {
	removed := false
	for _, name := range []string{
		fmt.Sprintf("staff-%d.p12", staffID),
		fmt.Sprintf("staff-%d.pass", staffID),
	} {
		err := os.Remove(filepath.Join(dir, name))
		if err == nil {
			removed = true
			continue
		}
		if os.IsNotExist(err) {
			continue
		}
		return err
	}
	if !removed {
		return os.ErrNotExist
	}
	return nil
}

func createSigningCertificate(dir string, staffID int64, commonName string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	caKey, caCert, err := loadOrCreateCA(dir)
	if err != nil {
		return "", err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", err
	}
	now := time.Now()
	cert := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: certificateCommonName(commonName), Organization: []string{"HSE Medpost"}},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.AddDate(2, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, caCert, &key.PublicKey, caKey)
	if err != nil {
		return "", err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return "", err
	}
	password, err := signingPassword()
	if err != nil {
		return "", err
	}
	pfx, err := pkcs12.Modern.Encode(key, leaf, []*x509.Certificate{caCert}, password)
	if err != nil {
		return "", err
	}
	p12Path := filepath.Join(dir, fmt.Sprintf("staff-%d.p12", staffID))
	passPath := filepath.Join(dir, fmt.Sprintf("staff-%d.pass", staffID))
	if err := os.WriteFile(p12Path, pfx, 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(passPath, []byte(password+"\n"), 0o600); err != nil {
		return "", err
	}
	return password, nil
}

func certificateCommonName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "HSE Medpost signer"
	}
	rs := []rune(name)
	if len(rs) > 64 {
		return string(rs[:64])
	}
	return name
}

func signingPassword() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	buf := make([]byte, 20)
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(buf), nil
}

func loadOrCreateCA(dir string) (*rsa.PrivateKey, *x509.Certificate, error) {
	keyPath := filepath.Join(dir, "ca.key")
	certPath := filepath.Join(dir, "ca.crt")
	keyPEM, keyErr := os.ReadFile(keyPath)
	certPEM, certErr := os.ReadFile(certPath)
	if keyErr == nil && certErr == nil {
		return parseCA(keyPEM, certPEM)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "HSE Medpost Certificate Authority", Organization: []string{"HSE Medpost"}},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	keyOut := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certOut := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(keyPath, keyOut, 0o600); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(certPath, certOut, 0o600); err != nil {
		return nil, nil, err
	}
	return key, cert, nil
}

func parseCA(keyPEM, certPEM []byte) (*rsa.PrivateKey, *x509.Certificate, error) {
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("ca key is not pem")
	}
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, fmt.Errorf("ca certificate is not pem")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return key, cert, nil
}
