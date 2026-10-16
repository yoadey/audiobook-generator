// Package feed serves a podcast RSS feed per book, so any podcast app can
// play the rendered chapters. Podcast apps cannot log in: a feed and its
// enclosures are authorized by a token (an HMAC of the book ID under a
// server secret) in the URL instead of a session.
package feed

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yoadey/audiobook-generator/internal/auth"
	"github.com/yoadey/audiobook-generator/internal/render"
	"github.com/yoadey/audiobook-generator/internal/store"
)

// Handler serves /feed/.
type Handler struct {
	Store *store.Store
	// Secret signs the feed tokens; empty disables feeds.
	Secret []byte
	// PublicURL is the external base URL ("https://audiobooks.example.org");
	// empty derives it from the request.
	PublicURL string
	DataDir   string
	Log       *slog.Logger
}

// Token authorizes the feed of one book.
func Token(secret []byte, bookID int64) string {
	m := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(m, "feed:%d", bookID)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

// Path is the feed's path, or "" when feeds are disabled.
func Path(secret []byte, bookID int64) string {
	if len(secret) == 0 {
		return ""
	}
	return fmt.Sprintf("/feed/%s/%d.xml", Token(secret, bookID), bookID)
}

// Register mounts the feed routes.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /feed/{token}/{file}", h.serveFeed)
	mux.HandleFunc("GET /feed/{token}/{bookId}/cover", h.serveCover)
	mux.HandleFunc("GET /feed/{token}/{bookId}/{chapterId}/{file}", h.serveAudio)
}

var (
	feedFile  = regexp.MustCompile(`^([0-9]{1,18})\.xml$`)
	audioFile = regexp.MustCompile(`^([0-9a-z]{1,20})\.m4a$`)
)

// authorized reports whether token belongs to the book.
func (h *Handler) authorized(token string, bookID int64) bool {
	return len(h.Secret) > 0 && hmac.Equal([]byte(token), []byte(Token(h.Secret, bookID)))
}

func (h *Handler) serveFeed(w http.ResponseWriter, r *http.Request) {
	m := feedFile.FindStringSubmatch(r.PathValue("file"))
	if m == nil {
		http.NotFound(w, r)
		return
	}
	bookID, _ := strconv.ParseInt(m[1], 10, 64)
	token := r.PathValue("token")
	if !h.authorized(token, bookID) {
		http.NotFound(w, r)
		return
	}
	b, err := h.Store.GetBook(r.Context(), bookID)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.fail(w, r, err)
		return
	}
	data, err := h.build(r.Context(), b, auth.BaseURL(r, h.PublicURL)+"/feed/"+url.PathEscape(token))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data) //nolint:gosec // encoding/xml escapes every value
}

func (h *Handler) serveAudio(w http.ResponseWriter, r *http.Request) {
	bookID, err1 := strconv.ParseInt(r.PathValue("bookId"), 10, 64)
	chapterID, err2 := strconv.ParseInt(r.PathValue("chapterId"), 10, 64)
	m := audioFile.FindStringSubmatch(r.PathValue("file"))
	if err1 != nil || err2 != nil || m == nil || !h.authorized(r.PathValue("token"), bookID) {
		http.NotFound(w, r)
		return
	}
	c, err := h.Store.GetChapter(r.Context(), chapterID)
	if errors.Is(err, store.ErrNotFound) || err == nil && (c.BookID != bookID || !playable(c)) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.fail(w, r, err)
		return
	}
	// The version the feed listed while it is kept, else the current one
	// (an app holding an older feed still gets the chapter).
	p := filepath.Join(h.DataDir, c.AudioPath)
	if v := m[1]; v != render.AudioVersion(c.AudioPath) {
		if old := filepath.Join(h.DataDir, render.VersionPath(bookID, chapterID, v)); exists(old) {
			p = old
		}
	}
	f, err := os.Open(p) //nolint:gosec // data dir + IDs + a version matching [0-9a-z]+
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.fail(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "audio/mp4")
	http.ServeContent(w, r, r.PathValue("file"), st.ModTime(), f)
}

func (h *Handler) serveCover(w http.ResponseWriter, r *http.Request) {
	bookID, err := strconv.ParseInt(r.PathValue("bookId"), 10, 64)
	if err != nil || !h.authorized(r.PathValue("token"), bookID) {
		http.NotFound(w, r)
		return
	}
	b, err := h.Store.GetBook(r.Context(), bookID)
	if errors.Is(err, store.ErrNotFound) || err == nil && b.CoverPath == "" {
		http.NotFound(w, r)
		return
	} else if err != nil {
		h.fail(w, r, err)
		return
	}
	if b.CoverMediaType != "" {
		w.Header().Set("Content-Type", b.CoverMediaType)
	}
	http.ServeFile(w, r, filepath.Join(h.DataDir, b.CoverPath))
}

func exists(p string) bool {
	_, err := os.Stat(p) //nolint:gosec // data dir + IDs + a version matching [0-9a-z]+
	return err == nil
}

func playable(c *store.Chapter) bool {
	return !c.Excluded && c.AudioPath != "" && c.AudioStatus != store.AudioNone
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	// Not the path: it carries the token.
	h.Log.ErrorContext(r.Context(), "feed failed", "book", r.PathValue("bookId"), "file", r.PathValue("file"), "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// RSS 2.0 with the iTunes podcast extensions.
type rss struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	ITunes  string   `xml:"xmlns:itunes,attr"`
	Channel channel  `xml:"channel"`
}

type channel struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Language    string `xml:"language,omitempty"`
	Author      string `xml:"itunes:author,omitempty"`
	Type        string `xml:"itunes:type"`
	Image       *image `xml:"itunes:image,omitempty"`
	Block       string `xml:"itunes:block"`
	Items       []item `xml:"item"`
}

type image struct {
	Href string `xml:"href,attr"`
}

type item struct {
	Title     string    `xml:"title"`
	GUID      guid      `xml:"guid"`
	PubDate   string    `xml:"pubDate"`
	Enclosure enclosure `xml:"enclosure"`
	Duration  string    `xml:"itunes:duration,omitempty"`
	Episode   int       `xml:"itunes:episode"`
	Season    int       `xml:"itunes:season,omitempty"`
}

type guid struct {
	IsPermaLink bool   `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

type enclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

// build renders the feed of b; base is the token's URL prefix.
func (h *Handler) build(ctx context.Context, b *store.Book, base string) ([]byte, error) {
	chapters, _, err := h.Store.ListChapters(ctx, b.ID, store.ChapterFilter{}, 0, 1<<30)
	if err != nil {
		return nil, err
	}
	vols, err := h.Store.ListVolumes(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	season := make(map[int64]int, len(vols))
	for _, v := range vols {
		season[v.ID] = v.Idx + 1
	}
	ch := channel{
		Title:       b.Title,
		Link:        siteURL(base),
		Description: "Audiobook of " + b.Title + rawAuthor(b.Author),
		Language:    b.Language,
		Author:      b.Author,
		Type:        "serial",
		// Private feeds must not be listed in podcast directories.
		Block: "yes",
	}
	if b.CoverPath != "" {
		ch.Image = &image{Href: fmt.Sprintf("%s/%d/cover", base, b.ID)}
	}
	for i := range chapters {
		c := &chapters[i]
		if !playable(c) {
			continue
		}
		version := render.AudioVersion(c.AudioPath)
		if version == "" {
			version = "current" // audio from before versioned files
		}
		it := item{
			Title: c.Title,
			// Stable across re-renders: an app updates the episode
			// instead of listing it twice.
			GUID: guid{Value: fmt.Sprintf("abg-chapter-%d", c.ID)},
			// Increasing dates keep the reading order in apps that sort
			// by date.
			PubDate: b.CreatedAt.UTC().Add(time.Duration(c.Idx) * time.Minute).Format(time.RFC1123Z),
			Enclosure: enclosure{
				URL:    fmt.Sprintf("%s/%d/%d/%s.m4a", base, b.ID, c.ID, version),
				Length: c.AudioSize,
				Type:   "audio/mp4",
			},
			Episode: c.Idx + 1,
		}
		if c.AudioDurationMs > 0 {
			it.Duration = strconv.FormatInt((c.AudioDurationMs+500)/1000, 10)
		}
		if c.VolumeID != nil {
			it.Season = season[*c.VolumeID]
		}
		ch.Items = append(ch.Items, it)
	}
	out, err := xml.MarshalIndent(rss{
		Version: "2.0", ITunes: "http://www.itunes.com/dtds/podcast-1.0.dtd", Channel: ch,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}

func rawAuthor(a string) string {
	if a == "" {
		return ""
	}
	return " by " + a
}

// siteURL is the web UI's URL for a feed base ".../feed/<token>".
func siteURL(base string) string {
	if i := strings.LastIndex(base, "/feed/"); i >= 0 {
		return base[:i] + "/"
	}
	return base
}
