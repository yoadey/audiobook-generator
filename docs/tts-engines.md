# Speech engines besides Kokoro

Kokoro (82M, CPU) narrates everything by default. It is fast and clear but
cannot act: emotions are given by pace and loudness, moods and pauses by
the text (see `PLAN.md` §3.6a, §3.7). Additional engines can be configured
for voices that should really act, typically a few main characters, while
Kokoro keeps the narration (most of a book).

## How engines are used

- An engine is configured by name; its voices are written
  `<name>:<voice>`, e.g. `chatterbox:Emily.wav`. Voices without a prefix
  are Kokoro's. Engine voices stand alone; they are not mixed with others.
- The voice editor of a character offers *Kokoro* or each engine. It lists
  the engine's voices, previews them, with an emotion if the engine speaks
  emotions itself. A narrator voice of an engine can be set through the
  API (`PATCH /api/books/{id}`), though narrating a whole book with a slow
  engine is rarely sensible.
- A voice whose engine is not configured is refused when saved; lines of
  such a voice (left from an earlier configuration) fail to render with a
  clear message.
- Rendering sends each piece to its voice's engine, at most `concurrency`
  at a time per engine; Kokoro keeps its own `ABG_TTS_CONCURRENCY`, so a
  slow engine does not hold Kokoro up. An engine that speaks emotions (`emotions: true`
  in `GET /api/tts/engines`) gets the quote's emotion, the narration's
  mood and whether it is a thought, and the render does not change its
  pace for them (it still makes whispers softer and shouts louder). An
  engine that does not take a speed is left at its own pace.
- Engines report no word timings: the reader marks their lines by
  sentence, the podcast transcript times them by segment.
- Changing an engine's settings (URL, variant, model, gain) affects
  chapters rendered afterwards; render a chapter again to apply them to
  it.
- Audio is converted to Kokoro's format (24 kHz mono 16-bit) from any WAV;
  `gain` evens out an engine's loudness against Kokoro's.

## Configuration

`ABG_TTS_ENGINES` is a JSON list (the chart builds it from `tts.engines`
and `tts.chatterbox`):

```json
[
  {"name": "chatterbox", "kind": "chatterbox", "url": "http://chatterbox:8004",
   "variant": "original", "concurrency": 1, "timeout": "10m", "gain": 1},
  {"name": "orpheus", "kind": "openai", "url": "http://orpheus:5005", "model": "orpheus"}
]
```

| Field | Meaning |
|---|---|
| `name` | Prefix of its voices: lower-case letters, digits, dashes |
| `kind` | `chatterbox` (Chatterbox-TTS-Server) or `openai` (any OpenAI-compatible `/v1/audio/speech` returning WAV) |
| `url` | Base URL |
| `variant` | Chatterbox only: `original` (default; acts emotions), `turbo` (faster, ignores them) or `nano` (fastest, ignores them; needs the patched image below) — must match the server's model |
| `model` | OpenAI-compatible only: `model` sent (default `tts-1`) |
| `concurrency` | Parallel requests (default 1) |
| `timeout` | Per request (default `10m`) |
| `gain` | Loudness factor (default 1; never clips) |
| `speed` | Whether it takes a voice's speed (default: `openai` yes, `chatterbox` no) |
| `voices` | Voices to offer in addition to those the server lists |

Voices are listed from the server: Chatterbox-TTS-Server's predefined
voices (`/get_predefined_voices`) and reference clips
(`/get_reference_files`, cloned), or `/v1/audio/voices` of an
OpenAI-compatible server.

The chart can run Chatterbox-TTS-Server itself (`tts.chatterbox.enabled`,
off by default): image `ghcr.io/devnen/chatterbox-tts-server`, pinned by
commit (`sha-915ae28-cpu`; use the `-nvidia`, `-cu128`, `-cu130` or `-rocm`
tag and a GPU resource for real use), a volume for the model (downloaded
from Hugging Face on the first start) and the reference clips, and its
`config.yaml` from the values (model by `variant`).

devnen's server cannot load Nano (its issue #162), so `variant: nano`
runs `tts.chatterbox.nanoImage`, built from `images/chatterbox-nano` and
released with the app (`ghcr.io/yoadey/audiobook-generator-chatterbox-nano`,
tag = app version): devnen's CPU image, upstream chatterbox at a pinned
commit, and `patch_nano.py`, which adds the `chatterbox-nano` model
selector (Turbo's class with `nano=True`) and casts the reference clip
back to float32 (numpy 2 in devnen's image otherwise turns it float64
and cloning fails). Every replacement of the patch must match exactly
once, so a changed server fails the image build (CI builds it on every
PR) rather than quietly loading Turbo. Bump the base image and
`CHATTERBOX_REF` together by hand. CPU only: for a GPU use `turbo` or
`original` with devnen's GPU images.

Upload reference clips
(10–30 s of clean speech, WAV or MP3) through the server's own UI
(`kubectl port-forward svc/<release>-chatterbox 8004`) or
`POST /upload_reference`; they appear as voices of the engine (on the
volume; predefined voices added in `/app/voices` are not kept). The
server synthesizes one request at a time and answers nothing else
meanwhile, so keep `concurrency: 1`; its probes only check the port, and
the app lists its voices at most once a minute (a failed listing is
retried after 30 s; a voice is never guessed to be predefined or cloned
before it was listed once). Renovate does not bump the image (its tags
are commit hashes): bump `tts.chatterbox.image.tag` by hand.

## Chatterbox: evaluation (October 2026)

Chatterbox (Resemble AI, MIT) is the most practical open model with an
emotion control. Facts below are from its source and READMEs unless
marked otherwise.

**Variants.** *Original* 0.5B (English) and *Multilingual* 0.5B take
`exaggeration` (0.25–2, neutral 0.5) and `cfg_weight` (0–1, 0.5; lower
is slower and more deliberate; the README advises exaggeration ≥ 0.7 with
cfg ≈ 0.3 for dramatic speech). *Turbo* 350M and *Nano* 110M (July 2026)
ignore both and act through inline tags (`[laugh]`, `[sigh]`, `[cough]`,
…). Output is 24 kHz mono. One generation is at most about 40 s of
speech, so text is chunked. A Perth watermark is applied to every output.

**What the app uses.** With `variant: original`, emotions map to
exaggeration/cfg (`internal/tts/remote.go`): e.g. neutral 0.5/0.5, angry
0.85/0.35, shouting 1.0/0.3, whispering 0.35/0.5; moods of narration
calm 0.4, tense 0.6, action 0.7. Each voice gets a fixed seed so it sounds
the same from piece to piece. Turbo's tags are not used: they would add
sounds that are not in the book, and the app never adds to the text.

**Voices.** Zero-shot cloning from a reference clip (Turbo needs more
than 5 s; the first 10–15 s are used). Chatterbox-TTS-Server ships 28
predefined voices. For dozens of characters, clips can be recorded or
generated (e.g. from Kokoro voices, though Chatterbox copies the clip's
delivery, so flat clips give flatter acting — an inference, untested).

**Speed and hardware.** The model runs on CUDA, ROCm, Apple MPS or CPU;
there is no Intel GPU (XPU/OpenVINO) path. Reported numbers
(benchmarks and issues, not measured here):

| Setup | Speed | 1M words (~100 h of audio) |
|---|---|---|
| Kokoro, CPU | ~1.3 CPU-core-hours per audio hour | the current setup |
| Turbo, CPU (PyTorch) | ~13 CPU-core-hours per audio hour (~10× Kokoro) | 3–4 days on 3 nodes |
| Original, CPU | far slower | not viable |
| Original, RTX 3060/4060 (stock) | ~1.5–4× real time | ~25–60 h |
| Original, RTX 3090/4090, optimised (vLLM fork) | ~10–30× real time | ~3–12 h |
| Nano, 8 CPU cores (vendor claim) | ~3× real time | ~11–15 h on 3 nodes |

Measured on the cluster (October 2026, one 4-vCPU worker with AVX2, CPU
only, model loaded, compute time ÷ audio length; lower is faster):

| Engine | Factor |
|---|---|
| Kokoro (Kokoro-FastAPI) | 0.23 |
| Nano | 0.9 |
| Turbo | 2.1 |

A VM without AVX2 (Proxmox's default CPU type) is about twice as slow
(Nano 2.0, with the library and through the patched server alike), so
pin Chatterbox to a node whose CPU type passes AVX2 through.

So on the cluster's CPUs Chatterbox is only sensible for a few main
characters' lines (a small share of a book) or with Nano/Turbo, which do
not act emotions by exaggeration. Real use needs an NVIDIA GPU with
12–24 GB (a used RTX 3090 is the sweet spot).

**Long-form quality.** The original model sometimes repeats or
hallucinates at the end of an utterance; Multilingual V3 and the
server's chunking, fixed seeds and trimming reduce it. The server splits
only between sentences, so a single sentence longer than about 40 s of
speech (roughly 600 characters) may be cut off. A safeguard worth
adding later: check each piece with speech recognition and re-generate
when the words do not match.

**Servers.** [devnen/Chatterbox-TTS-Server](https://github.com/devnen/Chatterbox-TTS-Server)
(MIT, v2, maintained, images for CPU/CUDA/ROCm) is the integration
target: its `POST /tts` takes `text`, `voice_mode` (`predefined` |
`clone`), `predefined_voice_id` / `reference_audio_filename`,
`exaggeration`, `cfg_weight`, `temperature`, `seed`, `speed_factor`
(time-stretch), `split_text`/`chunk_size` and `output_format`. Its
OpenAI-compatible endpoint ignores the emotion controls.
travisvn/chatterbox-tts-api has emotion controls on its OpenAI endpoint
but is unmaintained since December 2025 and has no Turbo. No server
reports word timings; there is no Helm chart (the app's chart wraps the
image).

## Other expressive models (brief)

| Model | Notes |
|---|---|
| Qwen3-TTS 0.6B/1.7B | emotions by instruction, voice design; GPU |
| IndexTTS-2.5 | strongest emotion/timbre control; bilibili licence |
| Dia2 1B/2B | Apache-2.0, dialogue, ≤ 2 min per generation |
| Orpheus 3B | emotion tags, OpenAI-compatible servers (usable as `kind: openai`); GPU |
| Higgs Audio v3, Fish S2 | non-commercial or restrictive licences |
| Pocket TTS 100M | CPU, cloning, no emotion control |

Any of them behind an OpenAI-compatible server returning WAV can be added
as `kind: openai` (pace and loudness for emotions, as with Kokoro).
