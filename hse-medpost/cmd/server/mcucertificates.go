package main

import (
	"context"
	"errors"
	"net/http"

	"hse-medpost/internal/entrymcu"
	"hse-medpost/internal/mcusubmit"
)

// portalCertificates lets the public portal ask for a health certificate without importing the
// clinical code: it is the same file staff download, built by the same rules.
type portalCertificates struct {
	api *entrymcu.API
	cfg entrymcu.CertConfig
}

func (p portalCertificates) Certificate(_ context.Context, mcuRecordID int64) (mcusubmit.CertificateFile, error) {
	// staffID 0: the certificate log records "not a staff member" as NULL.
	path, name, err := p.api.CertificateFile(mcuRecordID, 0, p.cfg)
	var refused *entrymcu.CertificateError
	switch {
	case err == nil:
		return mcusubmit.CertificateFile{Path: path, Filename: name}, nil
	case errors.As(err, &refused) && refused.Status == http.StatusNotFound:
		return mcusubmit.CertificateFile{}, &mcusubmit.CertificateUnavailable{Reason: "the checkup record could not be found"}
	case errors.As(err, &refused):
		return mcusubmit.CertificateFile{}, &mcusubmit.CertificateUnavailable{Reason: refused.Message}
	default:
		return mcusubmit.CertificateFile{}, err
	}
}
