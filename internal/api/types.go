package api

// PlayInfo is the top-level result of FetchPlayInfo for a single page.
//
// It aggregates the human-visible metadata (Title, Parts) plus the raw stream
// URLs returned by Bilibili's DASH playurl endpoint. Exactly one stream list
// is usually populated for video and audio respectively; subtitles may be
// empty for content that has none.
type PlayInfo struct {
	// Title is the series or video title surfaced by the metadata endpoint.
	Title string
	// BVID is populated for regular videos; empty for bangumi and courses.
	BVID string
	// AID is the numeric archive id. Populated for all kinds when available.
	AID string
	// CID identifies the concrete page (video file) whose streams are in
	// Videos/Audios. Always populated on a successful response.
	CID string
	// EPID is populated for bangumi and cheese episodes.
	EPID string
	// Parts enumerates every page of the item, including the one being
	// fetched. Callers use this for interactive selection and for multi-part
	// filename templating.
	Parts []Part
	// Videos lists DASH video streams for the selected page.
	Videos []Stream
	// Audios lists DASH audio streams for the selected page.
	Audios []Stream
	// Subtitles lists available subtitle tracks. URLs point to BCC JSON that
	// can be converted to SRT via FetchSubtitle.
	Subtitles []Subtitle
}

// Part describes a single page (分P) of an item. Order matches the API.
type Part struct {
	// Page is the 1-based index shown in the Bilibili player.
	Page int
	// CID is the page-level content id, used to request playurls.
	CID string
	// Title is the human-readable part title (may be empty).
	Title string
	// Duration is the declared length in seconds; 0 if unknown.
	Duration int
}

// Stream is one DASH track (either video or audio).
type Stream struct {
	// ID is the Bilibili-assigned quality id (e.g. 80 for 1080P). For audio
	// streams this is the audio id (e.g. 30280).
	ID int
	// BaseURL is the primary CDN URL.
	BaseURL string
	// BackupURLs is the list of fall-back CDN URLs returned by the API.
	BackupURLs []string
	// Bandwidth is the declared bits-per-second, copied verbatim from the API.
	Bandwidth int
	// Codecs is the DASH codec string (e.g. "avc1.640032").
	Codecs string
	// MimeType is the DASH mime type (e.g. "video/mp4").
	MimeType string
	// Quality is the human-visible label ("1080P 60") when one can be
	// resolved from accept_quality/accept_description; empty otherwise.
	Quality string
}

// Subtitle is an available subtitle track.
type Subtitle struct {
	// Lang is the language code Bilibili uses (e.g. "zh-CN").
	Lang string
	// URL is an absolute URL to the BCC JSON document.
	URL string
}
