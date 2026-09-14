package email

import "regexp"

var sixDigit = regexp.MustCompile(`\b(\d{6})\b`)

// codeFrom pulls the first plausible 6-digit code out of text. 177010 is a
// constant that shows up in provider chrome rather than in real mail, so it is
// skipped (the previous scraper excluded it for the same reason).
func codeFrom(text string) string {
	for _, m := range sixDigit.FindAllStringSubmatch(text, -1) {
		if len(m) > 1 && m[1] != "177010" {
			return m[1]
		}
	}
	return ""
}
