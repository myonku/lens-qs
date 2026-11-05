package sample

// Mapping is the nullable protocol foundation for B-05. The pure parser remains
// an implementation detail; its zero values must not become public assertions.
type Mapping struct {
	Project   *string `json:"sample_project"`
	Model     *string `json:"sample_model"`
	Spec      *string `json:"sample_spec"`
	Size      *string `json:"sample_size"`
	Side      *string `json:"sample_side"`
	Class     *string `json:"sample_class"`
	IsRx      *bool   `json:"is_rx"`
	DrawingID *string `json:"drawing_id"`
}
type CacheMetadata struct {
	ParserVersion     string  `json:"parser_version"`
	DictionaryVersion string  `json:"dictionary_version"`
	VerifiedBy        *string `json:"verified_by"`
	VerifiedAt        *string `json:"verified_at"`
	NeedsReview       bool    `json:"needs_review"`
}
