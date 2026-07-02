package server

// DetectedPrinter is the info returned for each OS-detected printer.
type DetectedPrinter struct {
	Name          string `json:"name"`
	MakeAndModel  string `json:"make_and_model,omitempty"`
	SuggestedType string `json:"suggested_type"`
}
