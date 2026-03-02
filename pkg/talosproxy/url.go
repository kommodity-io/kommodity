package talosproxy

import "net/url"

// parseURL parses a raw URL string into a *url.URL, returning nil on error.
func parseURL(rawURL string) *url.URL {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}

	return u
}
