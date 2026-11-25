package model

func AutoInputBand(tokens int64) string {
	switch {
	case tokens <= 0:
		return ""
	case tokens < 2000:
		return "short"
	case tokens < 16000:
		return "medium"
	case tokens < 64000:
		return "long"
	default:
		return "xlong"
	}
}
