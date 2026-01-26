package audio

// Metadata holds audio file metadata for future use.
// This is a stub for future extensibility to support metadata preservation
// during repair operations.
type Metadata struct {
	// Basic track info
	Title  string
	Artist string
	Album  string

	// Additional tags (format-specific)
	Tags map[string]string
}

// NewMetadata creates a new empty Metadata instance.
func NewMetadata() *Metadata {
	return &Metadata{
		Tags: make(map[string]string),
	}
}
