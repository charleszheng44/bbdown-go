package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/charleszheng44/bbdown-go/internal/parser"
)

// FetchPlayInfo fetches metadata and stream URLs for the page-th page of t.
// page is 1-indexed; 1 selects the first (and often only) page.
//
// Dispatch is by Kind:
//
//   - KindRegular -> x/web-interface/view  + x/player/wbi/playurl (WBI signed).
//   - KindBangumi -> pgc/view/web/season   + pgc/player/web/playurl.
//   - KindCourse  -> pugv/view/web/season  + pugv/player/web/playurl.
func (c *Client) FetchPlayInfo(ctx context.Context, t parser.Target, page int) (PlayInfo, error) {
	if page <= 0 {
		page = 1
	}
	switch t.Kind {
	case parser.KindRegular:
		return c.fetchRegular(ctx, t, page)
	case parser.KindBangumi:
		return c.fetchBangumi(ctx, t, page)
	case parser.KindCourse:
		return c.fetchCourse(ctx, t, page)
	default:
		return PlayInfo{}, fmt.Errorf("%w: unknown kind %v", ErrUnknownResponse, t.Kind)
	}
}

// ─── regular (BV / av) ────────────────────────────────────────────────────

// regularView is the decoded data field of x/web-interface/view.
type regularView struct {
	BVID  string `json:"bvid"`
	AID   int64  `json:"aid"`
	CID   int64  `json:"cid"`
	Title string `json:"title"`
	Pages []struct {
		Page     int    `json:"page"`
		CID      int64  `json:"cid"`
		Part     string `json:"part"`
		Duration int    `json:"duration"`
	} `json:"pages"`
	Subtitle struct {
		List []struct {
			Lan string `json:"lan"`
			URL string `json:"subtitle_url"`
		} `json:"list"`
	} `json:"subtitle"`
}

func (c *Client) fetchRegular(ctx context.Context, t parser.Target, page int) (PlayInfo, error) {
	// Metadata.
	q := map[string]string{}
	if t.BVID != "" {
		q["bvid"] = t.BVID
	} else if t.AID != "" {
		q["aid"] = t.AID
	} else {
		return PlayInfo{}, fmt.Errorf("%w: regular target missing bvid and aid", ErrUnknownResponse)
	}
	viewURL := apiBase + "/x/web-interface/view?" + encodeQuery(q)
	raw, err := c.doJSON(ctx, viewURL)
	if err != nil {
		return PlayInfo{}, err
	}
	var v regularView
	if err := json.Unmarshal(raw, &v); err != nil {
		return PlayInfo{}, fmt.Errorf("%w: decode view: %v", ErrUnknownResponse, err)
	}

	info := PlayInfo{
		Title: v.Title,
		BVID:  v.BVID,
		AID:   strconv.FormatInt(v.AID, 10),
	}
	for _, p := range v.Pages {
		info.Parts = append(info.Parts, Part{
			Page:     p.Page,
			CID:      strconv.FormatInt(p.CID, 10),
			Title:    p.Part,
			Duration: p.Duration,
		})
	}
	for _, s := range v.Subtitle.List {
		info.Subtitles = append(info.Subtitles, Subtitle{Lang: s.Lan, URL: normalizeSubURL(s.URL)})
	}

	// Pick the requested page.
	if page > len(info.Parts) {
		return PlayInfo{}, fmt.Errorf("%w: page %d out of range (have %d)", ErrUnknownResponse, page, len(info.Parts))
	}
	var cid string
	if len(info.Parts) > 0 {
		cid = info.Parts[page-1].CID
	} else {
		cid = strconv.FormatInt(v.CID, 10)
	}
	info.CID = cid

	// Stream URLs via WBI-signed playurl.
	mixin, err := c.mixinKeyLocked(ctx)
	if err != nil {
		return PlayInfo{}, err
	}
	playParams := map[string]string{
		"bvid":  v.BVID,
		"cid":   cid,
		"qn":    "0",
		"fnval": "4048", // DASH + HDR + 4K + Dolby + 8K (a superset flag).
		"fnver": "0",
		"fourk": "1",
	}
	signed := signWBI(playParams, mixin, c.now())
	playURL := apiBase + "/x/player/wbi/playurl?" + signed
	playRaw, err := c.doJSON(ctx, playURL)
	if err != nil {
		return PlayInfo{}, err
	}
	if err := decodePlayurl(playRaw, &info); err != nil {
		return PlayInfo{}, err
	}
	return info, nil
}

// ─── bangumi (ep / ss) ────────────────────────────────────────────────────

// bangumiSeason is a minimal decoding of pgc/view/web/season's result field.
type bangumiSeason struct {
	SeasonID int64  `json:"season_id"`
	Title    string `json:"title"`
	Episodes []struct {
		EPID      int64  `json:"ep_id"`
		CID       int64  `json:"cid"`
		AID       int64  `json:"aid"`
		Title     string `json:"title"`
		LongTitle string `json:"long_title"`
	} `json:"episodes"`
}

func (c *Client) fetchBangumi(ctx context.Context, t parser.Target, page int) (PlayInfo, error) {
	q := map[string]string{}
	if t.EPID != "" {
		q["ep_id"] = t.EPID
	} else if t.SSID != "" {
		q["season_id"] = t.SSID
	} else {
		return PlayInfo{}, fmt.Errorf("%w: bangumi target missing ep_id and season_id", ErrUnknownResponse)
	}
	url := pgcBase + "/pgc/view/web/season?" + encodeQuery(q)
	raw, err := c.doJSON(ctx, url)
	if err != nil {
		return PlayInfo{}, err
	}
	var s bangumiSeason
	if err := json.Unmarshal(raw, &s); err != nil {
		return PlayInfo{}, fmt.Errorf("%w: decode season: %v", ErrUnknownResponse, err)
	}

	info := PlayInfo{Title: s.Title}
	for i, e := range s.Episodes {
		info.Parts = append(info.Parts, Part{
			Page:  i + 1,
			CID:   strconv.FormatInt(e.CID, 10),
			Title: firstNonEmpty(e.LongTitle, e.Title),
		})
	}
	if len(s.Episodes) == 0 {
		return PlayInfo{}, fmt.Errorf("%w: bangumi season has no episodes", ErrUnknownResponse)
	}

	// If an ep_id was requested, prefer the matching episode; else use page.
	idx := -1
	if t.EPID != "" {
		for i, e := range s.Episodes {
			if strconv.FormatInt(e.EPID, 10) == t.EPID {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		if page > len(s.Episodes) {
			return PlayInfo{}, fmt.Errorf("%w: page %d out of range (have %d)", ErrUnknownResponse, page, len(s.Episodes))
		}
		idx = page - 1
	}
	ep := s.Episodes[idx]
	info.EPID = strconv.FormatInt(ep.EPID, 10)
	info.AID = strconv.FormatInt(ep.AID, 10)
	info.CID = strconv.FormatInt(ep.CID, 10)

	playQ := map[string]string{
		"ep_id": info.EPID,
		"cid":   info.CID,
		"qn":    "0",
		"fnval": "4048",
		"fnver": "0",
		"fourk": "1",
	}
	playURL := pgcBase + "/pgc/player/web/playurl?" + encodeQuery(playQ)
	playRaw, err := c.doJSON(ctx, playURL)
	if err != nil {
		return PlayInfo{}, err
	}
	// pgc playurl wraps the DASH payload under "result" -> the envelope's
	// payload() already handled that distinction, but some responses double-
	// wrap under a nested "video_info" key. Handle both.
	if err := decodePgcPlayurl(playRaw, &info); err != nil {
		return PlayInfo{}, err
	}
	return info, nil
}

// ─── cheese courses (pugv) ────────────────────────────────────────────────

// pugvSeason mirrors the relevant fields of pugv/view/web/season's data.
type pugvSeason struct {
	SeasonID int64  `json:"season_id"`
	Title    string `json:"title"`
	Episodes []struct {
		ID    int64  `json:"id"`
		EPID  int64  `json:"ep_id"`
		CID   int64  `json:"cid"`
		AID   int64  `json:"aid"`
		Title string `json:"title"`
	} `json:"episodes"`
}

func (c *Client) fetchCourse(ctx context.Context, t parser.Target, page int) (PlayInfo, error) {
	q := map[string]string{}
	if t.EPID != "" {
		q["ep_id"] = t.EPID
	} else if t.SSID != "" {
		q["season_id"] = t.SSID
	} else {
		return PlayInfo{}, fmt.Errorf("%w: course target missing ep_id and season_id", ErrUnknownResponse)
	}
	url := pugvBase + "/pugv/view/web/season?" + encodeQuery(q)
	raw, err := c.doJSON(ctx, url)
	if err != nil {
		return PlayInfo{}, err
	}
	var s pugvSeason
	if err := json.Unmarshal(raw, &s); err != nil {
		return PlayInfo{}, fmt.Errorf("%w: decode pugv season: %v", ErrUnknownResponse, err)
	}

	info := PlayInfo{Title: s.Title}
	for i, e := range s.Episodes {
		info.Parts = append(info.Parts, Part{
			Page:  i + 1,
			CID:   strconv.FormatInt(e.CID, 10),
			Title: e.Title,
		})
	}
	if len(s.Episodes) == 0 {
		return PlayInfo{}, fmt.Errorf("%w: course has no episodes", ErrUnknownResponse)
	}

	idx := -1
	if t.EPID != "" {
		for i, e := range s.Episodes {
			id := e.EPID
			if id == 0 {
				id = e.ID
			}
			if strconv.FormatInt(id, 10) == t.EPID {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		if page > len(s.Episodes) {
			return PlayInfo{}, fmt.Errorf("%w: page %d out of range (have %d)", ErrUnknownResponse, page, len(s.Episodes))
		}
		idx = page - 1
	}
	ep := s.Episodes[idx]
	epID := ep.EPID
	if epID == 0 {
		epID = ep.ID
	}
	info.EPID = strconv.FormatInt(epID, 10)
	info.AID = strconv.FormatInt(ep.AID, 10)
	info.CID = strconv.FormatInt(ep.CID, 10)

	playQ := map[string]string{
		"ep_id": info.EPID,
		"cid":   info.CID,
		"qn":    "0",
		"fnval": "4048",
		"fnver": "0",
		"fourk": "1",
	}
	playURL := pugvBase + "/pugv/player/web/playurl?" + encodeQuery(playQ)
	playRaw, err := c.doJSON(ctx, playURL)
	if err != nil {
		return PlayInfo{}, err
	}
	if err := decodePgcPlayurl(playRaw, &info); err != nil {
		return PlayInfo{}, err
	}
	return info, nil
}

// ─── playurl decoding ─────────────────────────────────────────────────────

// playurlDASH is the common DASH payload shape across all three endpoints.
type playurlDASH struct {
	AcceptQuality     []int    `json:"accept_quality"`
	AcceptDescription []string `json:"accept_description"`
	Dash              struct {
		Video []dashStream `json:"video"`
		Audio []dashStream `json:"audio"`
	} `json:"dash"`
}

type dashStream struct {
	ID         int      `json:"id"`
	BaseURL    string   `json:"baseUrl"`
	BaseURL2   string   `json:"base_url"` // snake_case seen on some responses
	BackupURL  []string `json:"backupUrl"`
	BackupURL2 []string `json:"backup_url"`
	Bandwidth  int      `json:"bandwidth"`
	Codecs     string   `json:"codecs"`
	MimeType   string   `json:"mimeType"`
	MimeType2  string   `json:"mime_type"`
}

// decodePlayurl fills info.Videos / info.Audios from the x/player/wbi/playurl
// payload.
func decodePlayurl(raw json.RawMessage, info *PlayInfo) error {
	var p playurlDASH
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("%w: decode playurl: %v", ErrUnknownResponse, err)
	}
	return fillStreams(&p, info)
}

// decodePgcPlayurl handles the pgc/pugv variants, whose payload sometimes
// nests the DASH data under "video_info". It also detects the preview-mode
// response — Bilibili returns code 0 with is_preview=1 and a durl-only MP4
// clip when the session is not entitled to the full content — and surfaces
// that as ErrContentLocked rather than leaking an empty-streams state to
// the planner.
func decodePgcPlayurl(raw json.RawMessage, info *PlayInfo) error {
	var peek struct {
		IsPreview int `json:"is_preview"`
	}
	if err := json.Unmarshal(raw, &peek); err == nil && peek.IsPreview == 1 {
		return fmt.Errorf("%w: playurl returned a preview clip; session is not entitled to this episode", ErrContentLocked)
	}

	// Try the flat shape first.
	var p playurlDASH
	if err := json.Unmarshal(raw, &p); err == nil && (len(p.Dash.Video) > 0 || len(p.Dash.Audio) > 0) {
		return fillStreams(&p, info)
	}
	// Fall back to the nested shape.
	var wrapped struct {
		VideoInfo playurlDASH `json:"video_info"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return fmt.Errorf("%w: decode pgc playurl: %v", ErrUnknownResponse, err)
	}
	if len(wrapped.VideoInfo.Dash.Video) == 0 && len(wrapped.VideoInfo.Dash.Audio) == 0 {
		return fmt.Errorf("%w: pgc playurl returned no DASH streams", ErrUnknownResponse)
	}
	return fillStreams(&wrapped.VideoInfo, info)
}

func fillStreams(p *playurlDASH, info *PlayInfo) error {
	qualityLabels := make(map[int]string, len(p.AcceptQuality))
	for i, q := range p.AcceptQuality {
		if i < len(p.AcceptDescription) {
			qualityLabels[q] = p.AcceptDescription[i]
		}
	}

	for _, s := range p.Dash.Video {
		info.Videos = append(info.Videos, toStream(s, qualityLabels))
	}
	for _, s := range p.Dash.Audio {
		info.Audios = append(info.Audios, toStream(s, nil))
	}
	return nil
}

func toStream(s dashStream, qualityLabels map[int]string) Stream {
	base := s.BaseURL
	if base == "" {
		base = s.BaseURL2
	}
	backup := s.BackupURL
	if len(backup) == 0 {
		backup = s.BackupURL2
	}
	mt := s.MimeType
	if mt == "" {
		mt = s.MimeType2
	}
	out := Stream{
		ID:         s.ID,
		BaseURL:    base,
		BackupURLs: backup,
		Bandwidth:  s.Bandwidth,
		Codecs:     s.Codecs,
		MimeType:   mt,
	}
	if qualityLabels != nil {
		out.Quality = qualityLabels[s.ID]
	}
	return out
}

// ─── helpers ──────────────────────────────────────────────────────────────

// encodeQuery sorts params by key and percent-encodes values, producing a
// stable query string without a leading '?'. Sort order is not required by
// the API but keeps URLs reproducible in tests.
func encodeQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b []byte
	for i, k := range keys {
		if i > 0 {
			b = append(b, '&')
		}
		b = append(b, wbiEscape(k)...)
		b = append(b, '=')
		b = append(b, wbiEscape(params[k])...)
	}
	return string(b)
}

// normalizeSubURL promotes protocol-relative //i0.hdslb.com URLs to https.
func normalizeSubURL(u string) string {
	if len(u) >= 2 && u[0] == '/' && u[1] == '/' {
		return "https:" + u
	}
	return u
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
