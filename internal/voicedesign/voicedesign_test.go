package voicedesign_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yoadey/audiobook-generator/internal/jobs"
	"github.com/yoadey/audiobook-generator/internal/kube"
	"github.com/yoadey/audiobook-generator/internal/store"
	"github.com/yoadey/audiobook-generator/internal/store/storetest"
	"github.com/yoadey/audiobook-generator/internal/voicedesign"
)

type enqueuer struct{ s *store.Store }

func (e *enqueuer) Enqueue(ctx context.Context, j *store.Job) error {
	j.Queue = voicedesign.Queue
	return e.s.EnqueueJob(ctx, j)
}

type uploads struct {
	mu    sync.Mutex
	names []string
}

func (u *uploads) UploadReference(_ context.Context, engine, name string, wav []byte) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if engine != "cb" || !strings.HasPrefix(string(wav), "RIFF") {
		return io.ErrUnexpectedEOF
	}
	u.names = append(u.names, name)
	return nil
}

type scaler struct {
	mu       sync.Mutex
	replicas int
	scales   []int
}

func (s *scaler) Scale(_ context.Context, n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replicas = n
	s.scales = append(s.scales, n)
	return nil
}

func (s *scaler) Status(context.Context) (kube.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return kube.Status{Replicas: s.replicas, Ready: s.replicas}, nil
}

func server(t *testing.T, got *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/design":
			raw, _ := io.ReadAll(r.Body)
			require.NoError(t, json.Unmarshal(raw, got))
			if (*got)["description"] == "refuse" {
				http.Error(w, "bad description", http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte("RIFF....WAVEfmt "))
		}
	}))
}

func TestDesignVoice(t *testing.T) {
	storetest.ForEachDialect(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		b := &store.Book{Title: "B", Status: store.BookReady}
		require.NoError(t, s.CreateBook(ctx, b))
		anna := &store.Character{BookID: b.ID, Name: "Anna", Gender: store.GenderFemale, AgeGroup: store.AgeElderly,
			SpeechStyle: "Slow and gruff", Role: "Innkeeper", Tier: store.TierMain, VoiceRecipe: "af_bella"}
		require.NoError(t, s.CreateCharacter(ctx, anna))
		var got map[string]any
		srv := server(t, &got)
		defer srv.Close()
		up, sc := &uploads{}, &scaler{}
		d := &voicedesign.Designer{
			Store: s, Jobs: &enqueuer{s}, Server: voicedesign.NewClient(srv.URL, time.Minute), Upload: up,
			Engine: "cb", Scaler: sc, Idle: time.Hour, ReadyTimeout: time.Minute, Log: slog.New(slog.DiscardHandler),
		}

		desc := voicedesign.DefaultDescription(anna)
		assert.Equal(t, "The voice of an elderly woman. Speaks slow and gruff. An innkeeper. "+
			"Clear, close recording, natural English.", desc)
		require.NoError(t, d.Start(ctx, anna, desc))
		assert.ErrorIs(t, d.Start(ctx, anna, desc), voicedesign.ErrBusy)
		require.NoError(t, d.Run(ctx, anna.ID, func(float64) {}))

		assert.Equal(t, desc, got["description"])
		assert.Equal(t, voicedesign.SampleText, got["text"])
		assert.Equal(t, []int{1}, sc.scales, "started on demand")
		require.Len(t, up.names, 1)
		assert.True(t, strings.HasPrefix(up.names[0], "f_b"), "gender tag for casting: %s", up.names[0])
		c, err := s.GetCharacter(ctx, anna.ID)
		require.NoError(t, err)
		assert.Equal(t, store.DesignDone, c.DesignStatus)
		assert.Equal(t, "cb:"+up.names[0], c.VoiceRecipe)
		assert.True(t, c.VoiceManual, "kept by the casting")
		assert.Contains(t, c.VoiceNote, "designed: ")

		// Idle and nothing pending: stopped. Designing again names a new clip.
		finish(t, s)
		d.Idle = 0
		ictx, cancel := context.WithCancel(ctx)
		go func() { time.Sleep(50 * time.Millisecond); cancel() }()
		d.Idler(ictx, 10*time.Millisecond)
		assert.Equal(t, []int{1, 0}, sc.scales)
		require.NoError(t, d.Start(ctx, c, "A young man's voice."))
		require.NoError(t, d.Run(ctx, anna.ID, func(float64) {}))
		require.Len(t, up.names, 2)
		assert.NotEqual(t, up.names[0], up.names[1])

		// A description the server refuses fails for good.
		finish(t, s)
		require.NoError(t, d.Start(ctx, c, "refuse"))
		err = d.Run(ctx, anna.ID, func(float64) {})
		var perm jobs.PermanentError
		require.ErrorAs(t, err, &perm)
		d.Handler().OnFail(ctx, &store.Job{Payload: "x"}, err)
		pending := &store.Job{Payload: itoa(anna.ID)}
		d.Handler().OnFail(ctx, pending, err)
		c, err = s.GetCharacter(ctx, anna.ID)
		require.NoError(t, err)
		assert.Equal(t, store.DesignFailed, c.DesignStatus)
		assert.Contains(t, c.DesignError, "bad description")
	})
}

func TestCleanDescription(t *testing.T) {
	d, msg := voicedesign.CleanDescription("  A  deep\nvoice ")
	assert.Equal(t, "A deep voice", d)
	assert.Empty(t, msg)
	_, msg = voicedesign.CleanDescription(" ")
	assert.NotEmpty(t, msg)
	_, msg = voicedesign.CleanDescription(strings.Repeat("x", voicedesign.MaxDescriptionRunes+1))
	assert.NotEmpty(t, msg)
}

func finish(t *testing.T, s *store.Store) {
	t.Helper()
	require.NoError(t, s.DB().Model(&store.Job{}).Where("status IN ?", []store.JobStatus{store.JobQueued, store.JobRunning}).
		Update("status", store.JobDone).Error)
}

func itoa(i int64) string {
	b, _ := json.Marshal(i)
	return string(b)
}
