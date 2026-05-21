package oneonefivecookie

import (
	"fmt"
	"strings"
)

func checkEnvelope[T any](envelope APIEnvelope[T], endpoint string) error {
	if envelope.Code != 0 || envelope.Errno != 0 || envelope.Error != "" || isNegativeAPIState(envelope.State) {
		return &APIError{
			URL:     endpoint,
			Code:    envelope.Code,
			Errno:   envelope.Errno,
			Message: firstNonEmpty(envelope.Error, envelope.Message, "115 API returned unsuccessful state"),
		}
	}
	return nil
}

func (d QRTokenData) Validate() error {
	var missing []string
	if strings.TrimSpace(d.UID) == "" {
		missing = append(missing, "uid")
	}
	if d.Time <= 0 {
		missing = append(missing, "time")
	}
	if strings.TrimSpace(d.Sign) == "" {
		missing = append(missing, "sign")
	}
	if len(missing) > 0 {
		return fmt.Errorf("115 qrcode token missing required field(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

func (c Cookie) Validate() error {
	var missing []string
	if strings.TrimSpace(c.UID) == "" {
		missing = append(missing, "UID")
	}
	if strings.TrimSpace(c.CID) == "" {
		missing = append(missing, "CID")
	}
	if strings.TrimSpace(c.SEID) == "" {
		missing = append(missing, "SEID")
	}
	if len(missing) > 0 {
		return fmt.Errorf("115 cookie missing required field(s): %s", strings.Join(missing, ", "))
	}
	return nil
}
