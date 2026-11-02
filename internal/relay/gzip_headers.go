package relay

import (
	"net/http"
	"strings"
)

// stripDecodedRequestHeaders removes inbound representation headers after decoding.
// Header.Del only removes canonical keys; middleware can insert noncanonical keys.
func stripDecodedRequestHeaders(headers http.Header) {
	for key := range headers {
		if strings.EqualFold(key, "Content-Encoding") || strings.EqualFold(key, "Content-Length") {
			delete(headers, key)
		}
	}
}
