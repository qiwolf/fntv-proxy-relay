package logger

import "net/url"

// SafeURL retains only the transport and authority. Paths can contain signed
// tokens too, so removing only the query or userinfo is not sufficient.
func SafeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "[redacted-url]"
	}
	return u.Scheme + "://" + u.Host
}
