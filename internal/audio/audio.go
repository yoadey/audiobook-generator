// Package audio assembles synthesized speech into chapter files with ffmpeg.
package audio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// PCM format of the input: signed 16-bit little-endian mono.
const (
	SampleRate     = 24000
	bytesPerSample = 2
)

// Silence returns d of silent PCM.
func Silence(d time.Duration) []byte {
	n := int(d.Milliseconds()) * SampleRate / 1000
	return make([]byte, n*bytesPerSample)
}

// Duration of a PCM byte count.
func Duration(pcmBytes int64) time.Duration {
	return time.Duration(pcmBytes/bytesPerSample) * time.Second / SampleRate
}

// Metadata is written into the output file.
type Metadata struct {
	Title  string // chapter title
	Album  string // book title
	Artist string // author
	Track  int    // 1-based chapter number
}

// Encoder runs ffmpeg.
type Encoder struct {
	FFmpeg string // path of the ffmpeg binary
	// Bitrate of the AAC output, e.g. "64k".
	Bitrate string
}

// Encode reads PCM from r and writes an AAC .m4a file to out (atomically:
// a temp file next to it is renamed on success).
func (e Encoder) Encode(ctx context.Context, r io.Reader, out string, m Metadata) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return err
	}
	tmp := out + ".tmp.m4a"
	bitrate := e.Bitrate
	if bitrate == "" {
		bitrate = "64k"
	}
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-f", "s16le", "-ar", strconv.Itoa(SampleRate), "-ac", "1", "-i", "pipe:0",
		// EBU R128 loudness normalisation; resample back after the filter.
		"-af", "loudnorm=I=-18:TP=-1.5:LRA=11",
		"-ar", strconv.Itoa(SampleRate),
		"-c:a", "aac", "-b:a", bitrate,
		"-movflags", "+faststart",
		"-metadata", "title=" + m.Title,
		"-metadata", "album=" + m.Album,
		"-metadata", "artist=" + m.Artist,
		"-metadata", "genre=Audiobook",
	}
	if m.Track > 0 {
		args = append(args, "-metadata", "track="+strconv.Itoa(m.Track))
	}
	args = append(args, tmp)

	cmd := exec.CommandContext(ctx, e.FFmpeg, args...) //nolint:gosec // fixed binary, arguments are not shell-interpreted
	cmd.Stdin = r
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("ffmpeg: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	return os.Rename(tmp, out)
}
