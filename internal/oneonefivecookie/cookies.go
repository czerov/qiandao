package oneonefivecookie

import (
	"fmt"
	"strings"
)

func FormatCookieText(cookie Cookie) string {
	text := fmt.Sprintf("UID=%s; CID=%s; SEID=%s", cookie.UID, cookie.CID, cookie.SEID)
	if strings.TrimSpace(cookie.KID) != "" {
		text += "; KID=" + cookie.KID
	}
	return text
}

func FormatCookieJSON(cookie Cookie) []CookieEntry {
	values := []struct {
		name  string
		value string
	}{
		{name: "UID", value: cookie.UID},
		{name: "CID", value: cookie.CID},
		{name: "SEID", value: cookie.SEID},
	}
	if strings.TrimSpace(cookie.KID) != "" {
		values = append(values, struct {
			name  string
			value string
		}{name: "KID", value: cookie.KID})
	}

	hosts := []string{"115.com"}
	entries := make([]CookieEntry, 0, len(values)*len(hosts))
	id := 1
	for _, item := range values {
		for _, host := range hosts {
			entries = append(entries, CookieEntry{
				Domain:   host,
				HostOnly: false,
				HTTPOnly: true,
				Name:     item.name,
				Path:     "/",
				SameSite: "unspecified",
				Secure:   false,
				Session:  false,
				StoreID:  "0",
				Value:    item.value,
				ID:       id,
			})
			id++
		}
	}
	return entries
}
