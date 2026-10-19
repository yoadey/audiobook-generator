package audio

import (
	"bytes"
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tone(d time.Duration) []byte {
	n := int(d.Milliseconds()) * SampleRate / 1000
	buf := make([]byte, 0, n*2)
	for i := range n {
		v := int16(8000 * math.Sin(2*math.Pi*440*float64(i)/SampleRate))
		buf = append(buf, byte(v), byte(v>>8))
	}
	return buf
}

func TestSilenceAndDuration(t *testing.T) {
	s := Silence(1500 * time.Millisecond)
	assert.Len(t, s, 36000*2)
	assert.Equal(t, 1500*time.Millisecond, Duration(int64(len(s))))
}

func TestEncode(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	pcm := append(tone(time.Second), Silence(500*time.Millisecond)...)
	out := filepath.Join(t.TempDir(), "audio", "1.m4a")
	err = Encoder{FFmpeg: ffmpeg}.Encode(context.Background(), bytes.NewReader(pcm), out, Metadata{
		Title: "Chapter 1", Album: "The Book", Artist: "Someone", Track: 1,
	})
	require.NoError(t, err)

	probe, err := exec.Command("ffprobe", "-v", "error", "-show_entries",
		"format=duration:format_tags=title,album:stream=codec_name,sample_rate",
		"-of", "default=nw=1", out).Output()
	require.NoError(t, err)
	info := string(probe)
	assert.Contains(t, info, "codec_name=aac")
	assert.Contains(t, info, "sample_rate=24000")
	assert.Contains(t, info, "TAG:title=Chapter 1")
	assert.Contains(t, info, "TAG:album=The Book")
	assert.True(t, strings.Contains(info, "duration=1.5"), info)
}

func TestEncodeFailureLeavesNoFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "x.m4a")
	err := Encoder{FFmpeg: "/nonexistent/ffmpeg"}.Encode(context.Background(), bytes.NewReader(nil), out, Metadata{})
	require.Error(t, err)
	assert.NoFileExists(t, out)
}

func TestMP3(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	data, err := Encoder{FFmpeg: "ffmpeg"}.MP3(context.Background(), Silence(500*time.Millisecond))
	require.NoError(t, err)
	// An MP3 starts with an ID3 tag or a frame sync.
	require.Greater(t, len(data), 3)
	assert.True(t, string(data[:3]) == "ID3" || (data[0] == 0xFF && data[1]&0xE0 == 0xE0), "an MP3")
}
