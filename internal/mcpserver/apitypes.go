package mcpserver

// apiSearchResult is the body of GET /kbs/{kb}/search, which is the search
// tool's result relayed as it is (the tool builds a map). It exists to type the
// route: TestAPIRoutes_BodiesDecodeIntoTheirResponseType fails when the tool
// writes a key this type lacks.
type apiSearchResult struct {
	Query            string       `json:"query"`
	Mode             string       `json:"mode"`
	Count            int          `json:"count"`
	Results          []searchHit  `json:"results"`
	ExpandedTo       []string     `json:"expanded_to,omitempty"`
	Related          []relatedHit `json:"related,omitempty"`
	ArchivedFallback bool         `json:"archived_fallback,omitempty"`
	OrFallback       bool         `json:"or_fallback,omitempty"`
	Note             string       `json:"note,omitempty"`
}
