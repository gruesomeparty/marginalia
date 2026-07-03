package document

// Block is a commentable top-level element of a document.
type Block struct {
	ID        string `json:"id"`
	Section   string `json:"section"`
	Ordinal   int    `json:"ordinal"`
	Kind      string `json:"kind"`
	Level     int    `json:"level"`
	Quote     string `json:"quote"`
	Hash      string `json:"hash"`
	HTML      string `json:"html"`
	PlainText string `json:"text"`
}

// Document is a parsed source document.
type Document struct {
	Path   string  `json:"path"`
	Blocks []Block `json:"blocks"`
}
