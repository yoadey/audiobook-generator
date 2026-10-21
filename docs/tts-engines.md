# Speech engines

Kokoro (82M, CPU) is fast and clear but cannot act: emotions are given by
pace and loudness, moods and pauses by the text (see `PLAN.md` §3.6a,
§3.7). Other engines, such as Chatterbox, Qwen3-TTS (directed by the
speaker attribution's instructions) or Gemini (Google AI Studio, in the
cloud), can be configured beside it or instead of it.

## Which engine a book speaks with

- The deployment decides which engines exist: Kokoro (the chart's or an
  external one; `ABG_TTS_KOKORO=false` without), the chart's Chatterbox,
  Gemini (`tts.gemini`), further engines (`tts.engines`), in any
  combination with at least one.
- A book speaks with the engine of its narrator. The audiobook page
  (*Narrator*) offers each engine of the deployment; switching starts from
  the engine's default voice, and saving casts the characters again from
  that engine's voices (voices you chose for characters stay).
- New books speak with the default engine: that of `ABG_TTS_DEFAULT_VOICE`
  (chart `tts.defaultVoice`). Without Kokoro, a Kokoro voice there stands
  for the first engine's default voice (`defaultVoice` of the engine, for
  Chatterbox `Emily.wav`, for Gemini `Kore`), so a deployment with only
  one of them needs no other setting.
- Casting with another engine gives each main character a voice of its
  own (engines' voices do not blend and keep their own pace), avoiding the
  narrator's; once every voice is taken, the least used one is shared.
  Minor characters get a voice of their gender by hash, as with Kokoro.
  A voice's gender comes from Chatterbox-TTS-Server's known predefined
  voices and Gemini's prebuilt voices (by name) or a tag in a clip's file
  name (`f_anna.wav`,
  `old-man.wav`, `female`, `male`); voices of unknown gender are used
  only when chosen by hand.
- A character voice whose engine is no longer configured is replaced by
  a pool voice when rendering.

## How engines are used

- An engine is configured by name; its voices are written
  `<name>:<voice>`, e.g. `chatterbox:Emily.wav`. Voices without a prefix
  are Kokoro's. Engine voices stand alone; they are not mixed with others.
- *Voices* lists each engine with its voices and previews them (with an
  emotion if the engine speaks emotions itself); *System* shows whether
  each engine is reachable.
- The voice editor of a character offers *Kokoro* (when deployed) or
  each engine. A slow engine narrating a whole book takes long without a
  GPU (see below).
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
- *Settings* (*Speech requests*) changes `maxChars` and `joinParagraphs`
  of each engine while running (kept across restarts; only what was
  changed overrides the configuration, *Reset* goes back to it). `maxChars`
  can be set up to 5000 for Gemini, 4000 for OpenAI-compatible servers
  and 10000 for Chatterbox. They apply to chapters rendered from then on;
  a chapter in the middle of rendering speaks its remaining text anew.
- Each request to an engine is as long as the text allows: the pieces of
  a paragraph that one voice speaks in one style (its sentences, a
  dialogue tag between them before the speakers are known) go out
  together, up to the engine's `maxChars`, so the engine reads them in
  one flow instead of piece by piece. A pause that means something (after
  a thought, a line trailing off, one the chapter preparation asks for)
  and another voice start a new request, and so does every paragraph end
  unless the chapter preparation found that the next paragraph flows on
  from it, or (`joinParagraphs`) whenever the next paragraph goes on in
  the same voice and style: joined paragraphs keep a blank line between
  them, and the engine's own pause replaces the app's. Speech the
  narrator reads without an emotion of its own (an
  unknown speaker, or every quotation in a book without character
  voices) is read like the narration around it, so it joins in too.
  Chatterbox-TTS-Server splits text longer than 450 characters (about
  30 s of speech) by sentences itself.
- Engines report no word timings: the reader marks their lines by
  sentence, the podcast transcript times them by segment (joined
  segments by their length).
- A Chatterbox engine that spoke nothing for `ABG_TTS_UNLOAD_IDLE`
  (chart `tts.unloadIdle`, default 10 minutes) has its model unloaded
  (`POST /api/unload`; Nano on CPU frees about 1.7 of 2.9 GiB). The next
  synthesis loads it again first (`POST /restart_server`, about 10 s),
  as does one the server refuses for want of a model. *Settings*
  (*Speech models*) turns this off; `0s` turns it off for the
  deployment. The server itself and its voices stay up: listing voices
  and uploading clips need no model.
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
| `kind` | `chatterbox` (Chatterbox-TTS-Server), `gemini` (the Gemini API, below), `qwen3` (a Qwen3-TTS server, below) or `openai` (any OpenAI-compatible `/v1/audio/speech` returning WAV) |
| `url` | Base URL (Gemini: default `https://generativelanguage.googleapis.com/v1beta`) |
| `variant` | Chatterbox only: `original` (default; acts emotions), `turbo` (faster, ignores them) or `nano` (fastest, ignores them; needs the patched image below) — must match the server's model |
| `model` | OpenAI-compatible: `model` sent (default `tts-1`); Gemini: the TTS model (default `gemini-3.1-flash-tts-preview`); Qwen3: `model` sent (default `Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice`) |
| `language` | Qwen3 only: the language it speaks (default `English`) |
| `concurrency` | Parallel requests (default 1) |
| `timeout` | Per request (default `10m`) |
| `gain` | Loudness factor (default 1; never clips) |
| `speed` | Whether it takes a voice's speed (default: `openai` yes, `chatterbox` no; `gemini` never) |
| `voices` | Voices to offer in addition to those the server lists |
| `maxChars` | Longest text sent in one request; the pieces of a paragraph in one voice are joined up to it (default: `chatterbox` and `qwen3` 1000, `gemini` 4000, `openai` 400) |
| `joinParagraphs` | Read consecutive paragraphs in one voice and style in one request, not only those the chapter preparation found to flow on (default: `gemini` and `qwen3` yes, others no) |
| `defaultVoice` | Narrator when a book switches to this engine (default: Chatterbox `Emily.wav`, Gemini `Kore`, Qwen3 `Ryan`, else the first of `voices`) |
| `apiKeyEnv` | Gemini only: environment variable with the API key (required) |
| `limits` | Gemini only: `rpm`, `tpm`, `rpd`, `tpd` of the model's quota (0: unknown; tokens are text tokens) |
| `quotaTimezone` | Gemini only: where the daily quota resets at midnight (default `America/Los_Angeles`) |
| `emotions` | Gemini and Qwen3: tell the engine how to speak each piece (the attribution's directions, emotions, thoughts, kinds of voice; default `false` for Gemini, `true` for Qwen3) |

Voices are listed from the server: Chatterbox-TTS-Server's predefined
voices (`/get_predefined_voices`) and reference clips
(`/get_reference_files`, cloned), or `/v1/audio/voices` of an
OpenAI-compatible server. Gemini's 30 prebuilt voices are known.

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

## Gemini (Google AI Studio)

The Gemini API speaks with 30 prebuilt voices (`Kore`, `Puck`, `Charon`,
…; 14 female, 16 male), in the cloud: no GPU, good prosody, but a quota.
Turn it on in the chart:

```yaml
tts:
  gemini:
    enabled: true
    apiKeySecret: { name: audiobook-llm-keys, key: gemini }  # an existing Secret
    limits: { rpm: 10, rpd: 100 }   # as AI Studio shows them for the model
```

The key is created in [Google AI Studio](https://aistudio.google.com/apikey);
the Secret of a Gemini LLM provider on the same project works too (quotas
are per model, so speech and analysis do not take from each other). The
app sends `POST {url}/models/{model}:generateContent` with
`responseModalities: ["AUDIO"]` and the voice as `prebuiltVoiceConfig`,
and turns the 24 kHz PCM answer into the chapter's audio like any other.
Previews are encoded to MP3 with ffmpeg.

**Quota.** Every request counts: each piece of a chapter and each voice
preview. To need few, a Gemini engine reads across paragraphs: consecutive
text in one voice and style goes out in one request of up to `maxChars`
(4000 characters by default, about four minutes of speech; both can be
changed in *Settings*, *Speech requests*). Every change of voice or of a
quote's emotion starts a new request, so a dialogue-heavy chapter with
character voices needs many more requests than one read by the narrator
alone (*Character voices* off on the book's *Narrator* card). The app counts requests and tokens per
day (`tts_usages`, in `quotaTimezone`, kept across restarts) and shows
them with the engine: *Voices* (requests and text tokens today, requests
per minute, against the limits), *System*, and the book's *Narrator*
card ("63 of 100 requests left today"). Google offers no way to ask for
the remaining quota, so what is left is counted against `limits`; when
they are not set, the limits Gemini names in its first 429 answer
(`QuotaFailure`) are used until a restart, and the display says so.
Requests wait for the per-minute window (up to 70 s; a preview 10 s,
and it is not queued behind a chapter's requests); beyond that, and once
the daily quota is used up (by `rpd`/`tpd` or a 429), rendering jobs are
deferred and go on when the quota resets (midnight Pacific time) or the
engine's retry hint has passed. A request Gemini refuses with 429 or 503
is not counted. An overloaded engine (503) makes every request wait 2
minutes (jobs are deferred, nothing is sent meanwhile); another server
error uses up an attempt (an error that
stays for one text must end); an answer without audio is asked for once
more; an answer longer than about 11 minutes of audio fails its chapter
(lower `maxChars`). A refused
API key or unknown model defers jobs for 15 minutes (fix the Secret and
restart); a text Gemini refuses to speak (safety filter) fails its chapter
with that reason. The pieces already spoken stay cached in every case,
also when a render fails, and a render that made progress does not use up
its job's attempts.

**Emotions.** With `emotions: true` a quote's emotion and thoughts are
asked for in Google's documented way: an instruction before the text
(`Say angrily: …`, `Whisper: …`, `Say softly, as an inner thought: …`);
where the speaker attribution wrote a direction or found another kind of
voice, that is said instead (`Say low and menacing: …`, `Say as a voice
heard through a phone, radio or loudspeaker, a little distant: …`), also
for narration with a direction of its own; the book's text follows
unchanged. Off by default, then emotions are given
by loudness like for Kokoro. Gemini takes no speed: it paces itself.

**Privacy.** The book text is sent to Google. On the free tier Google may
use it to improve its products; use a paid project if that matters.

## Qwen3-TTS: directed voices

[Qwen3-TTS](https://huggingface.co/Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice)
(Apache-2.0) follows instructions in words: emotion and its strength,
volume, pace, pitch, voice quality. That is what the speaker attribution
writes for every line (§3.6 of `PLAN.md`): a direction such as "barely
above a whisper, trembling with fear", and the kind of voice (speech,
thought, telepathy, off-voice, system message, writing read out, song);
the narration gets a direction where it should be read differently.

The app does **not** deploy Qwen3-TTS (it needs a GPU; the chart stays
as it is). Run a server with an OpenAI-compatible speech endpoint that
takes `instructions` yourself, e.g.
[vLLM-Omni](https://github.com/vllm-project/vllm-omni) with a CustomVoice
model, and point an engine at it:

```yaml
tts:
  engines:
    - name: qwen3
      kind: qwen3
      url: http://qwen3-tts.tts:8091
      # model: Qwen/Qwen3-TTS-12Hz-1.7B-CustomVoice (default)
      # language: English (default)
      concurrency: 2
```

Then switch a book's narrator (*Narrator*) to a `qwen3:` voice; the
characters are cast from the engine's voices. The app sends
`POST {url}/v1/audio/speech` with `model`, `input`, `voice`,
`language`, `response_format: wav` and, where there is anything to say,
`instructions`:

| Piece | `instructions` |
|---|---|
| a line with a direction | `Say it through clenched teeth, seething.` |
| a line without one | the emotion in words (`Say it angrily, with force and tension.`) |
| a thought, telepathy, an off-voice, … | the direction plus the kind (`Say it wistful, softly and intimately, as an inner thought.`) |
| narration | the paragraph's direction, else its mood from the chapter preparation; plain narration has none |

Each change of voice or instruction starts a new request; consecutive
text in one voice and style is joined up to `maxChars` (1000 by default,
about a minute of speech), across paragraphs too. Voices are listed from
`GET /v1/audio/voices`; a server that lists none offers the CustomVoice
speakers (Aiden, Dylan, Eric, Ryan, Uncle_Fu male; Ono_Anna, Serena,
Sohee, Vivian female), whose genders casting knows (in either case).
Cloned voices a server lists are used when their name carries a gender
tag (`f_anna`), as with Chatterbox. The engine takes no speed by default
(it paces itself as told); `speed: true` sends the voice's speed.
`emotions: false` sends no instructions (emotions are then given by
loudness, like for Kokoro). Changing the URL, model, language, gain or
these switches outdates nothing by itself: render a chapter again to
apply them.

## Voice design: voices from a description

Chatterbox clones voices from clips; it cannot invent one from a prompt.
Voice design fills that gap: a small server
([Qwen3-TTS-12Hz-1.7B-VoiceDesign](https://huggingface.co/Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign),
Apache-2.0) speaks a short narration in a voice described in words, and
that clip is uploaded to Chatterbox as a reference clip, which then
clones it for the whole book. The words of the book are never part of
this: the sample text is fixed.

- On a character's page, *Design a voice* takes a description (age,
  gender, timbre, accent, manner; up to 500 characters), prefilled from
  the character's profile (*From the profile*). *Design voices* on the
  casting card queues one for every main character whose voice was not
  chosen by hand.
- A designed voice becomes the character's voice as one chosen by hand
  (`chatterbox:f_b<book>-c<character>-<hash>.wav`); the casting keeps it.
  Designing again makes a new clip with a new seed, so the same
  description gives another take; earlier clips stay on Chatterbox's
  volume and are offered like any other reference clip (their gender
  tag lets casting use them for other books too).
- Only books that speak with the design engine (Chatterbox) can design
  voices; switch a Kokoro book to Chatterbox first.
- Each design is a persistent job (queue `design`, one at a time, three
  attempts). A description the server refuses fails at once; the error
  is shown on the character. Canceling the job (*Jobs*) marks the design
  failed, retrying it queues it again; choosing a voice by hand while a
  design is pending drops that design. A design left without a job (the
  app stopped, voice design turned off) is marked failed at startup.

### On demand, on CPU

The model runs on CPU: expect one to a few minutes per voice (a ~15 s
clip) and 10–16 GB of memory at `float32` (`dtype: bfloat16` halves the
memory, slower on CPUs without AMX). It is meant to run rarely, so by
default (`voiceDesign.onDemand: true`) the app runs it only while needed:

1. A design job scales the Deployment to one (`deployments/scale`) and
   waits until the pod is ready and `/health` answers (the model is
   loaded; the first start downloads it, about 4 GB, to its volume).
   Waiting longer than `readyTimeout` fails the attempt. A model that
   fails to load ends the server process, so the pod restarts.
2. While designs are pending, the pod keeps running.
3. Once no design is pending and none ran for `idle` (10 minutes), the
   app scales it back to zero. A pod left running when the app restarts
   is stopped the same way, as is the one Kubernetes starts at install
   (the chart sets no replica count, so upgrades never reset the live
   one).

For this a Role allows `patch` on `deployments/scale` and `get` on
`deployments`, both for exactly this one Deployment, bound to the app's
own ServiceAccount (the chart refuses the namespace's default one); the
token is mounted into the app container only.

```yaml
voiceDesign:
  enabled: true          # needs tts.chatterbox.enabled (or an engine of kind chatterbox, voiceDesign.engine)
  # resources.requests.memory 10Gi, limits.memory 16Gi by default
```

Helm, Flux and Argo CD leave the replica count to the app, since the
chart does not set it. With `onDemand: false` the server always runs (one
replica) and the app never touches the Kubernetes API.

Outside the chart, set `ABG_VOICE_DESIGN_URL` (the server),
`ABG_VOICE_DESIGN_ENGINE` (default `chatterbox`) and, for scaling,
`ABG_VOICE_DESIGN_DEPLOYMENT` (a Deployment of the app's namespace) plus
`ABG_VOICE_DESIGN_IDLE`, `_TIMEOUT` and `_READY_TIMEOUT`.

The image (`images/voice-design`, released with the app as
`ghcr.io/yoadey/audiobook-generator-voice-design`) is a FastAPI wrapper
around the `qwen-tts` package: `POST /design` with
`{"text", "description", "seed"}` answers a WAV (at most 29 s, peak
normalized, 16-bit; generation stops at 29 s of speech, so a run that
does not end cannot grow in time and memory), `GET /health` answers 503
until the model is loaded. `voiceDesign.modelRevision` pins the model's commit (the whole
repository is downloaded at that revision and loaded from disk). For local
runs, `make fake-tts` answers `/design` with a tone.

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
| Qwen3-TTS 0.6B/1.7B | emotions by instruction (supported: `kind: qwen3`, above), voice design; GPU |
| IndexTTS-2.5 | strongest emotion/timbre control; bilibili licence |
| Dia2 1B/2B | Apache-2.0, dialogue, ≤ 2 min per generation |
| Orpheus 3B | emotion tags, OpenAI-compatible servers (usable as `kind: openai`); GPU |
| Higgs Audio v3, Fish S2 | non-commercial or restrictive licences |
| Pocket TTS 100M | CPU, cloning, no emotion control |

Any of them behind an OpenAI-compatible server returning WAV can be added
as `kind: openai` (pace and loudness for emotions, as with Kokoro).
