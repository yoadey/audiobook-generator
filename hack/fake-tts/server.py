"""Minimal stand-in for Kokoro-FastAPI and an LLM provider, used by the e2e
test and local runs.

Kokoro: GET /health, GET /v1/audio/voices (v0.9 response shape),
POST /v1/audio/speech and POST /dev/captioned_speech (non-streaming, with
word timestamps). Speech is a quiet 220 Hz tone whose length grows with the
text (20 ms per character), as 24 kHz 16-bit mono PCM; any other
response_format gets a WAV file.

Chatterbox-TTS-Server: GET /get_predefined_voices, GET /get_reference_files,
POST /upload_reference (kept in memory) and POST /tts (a WAV tone, slower
with more exaggeration), so the app can be run with
ABG_TTS_ENGINES='[{"name":"chatterbox","kind":"chatterbox",
"url":"http://localhost:8880"}]'.

Voice design (images/voice-design): POST /design answers a 3 s WAV tone
(after FAKE_DESIGN_SECONDS of thinking, default 1), so the app can be run
with ABG_VOICE_DESIGN_URL=http://localhost:8880 and no deployment to scale.

LLM (OpenAI-compatible): GET /v1/models and POST /v1/chat/completions, which
answers the app's provider test prompt correctly, the character analysis
with everyone who "said" something, merge checks with "different", voice
casting with the first offered voice for everyone (code then makes the
voices distinct), speaker attribution by the "said Name" of the quote's
paragraph, chapter preparation with a pause after "said Anna." and a
pronunciation for Anna, and anything else with "{}". When FAKE_LLM_KEY is set, requests
must carry it as bearer token.
"""

import base64
import json
import math
import os
import re
import struct
import time
from email.parser import BytesParser
from email.policy import HTTP
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

RATE = 24000
VOICES = ["af_heart", "af_bella", "am_michael", "bf_emma", "bm_george"]
CHATTERBOX_VOICES = [{"display_name": "Emily", "filename": "Emily.wav"}, {"display_name": "Adrian", "filename": "Adrian.wav"}]
CHATTERBOX_REFERENCES = ["anna.wav"]
LLM_KEY = os.environ.get("FAKE_LLM_KEY", "")
DESIGN_SECONDS = float(os.environ.get("FAKE_DESIGN_SECONDS", "1"))
MODELS = ["fake-small", "fake-large"]


def tone(seconds: float) -> bytes:
    n = int(seconds * RATE)
    return b"".join(
        struct.pack("<h", int(2000 * math.sin(2 * math.pi * 220 * i / RATE))) for i in range(n)
    )


def wav(pcm: bytes) -> bytes:
    header = struct.pack(
        "<4sI4s4sIHHIIHH4sI",
        b"RIFF", 36 + len(pcm), b"WAVE", b"fmt ", 16, 1, 1, RATE, RATE * 2, 2, 16, b"data", len(pcm),
    )
    return header + pcm


def analyse(text: str) -> str:
    """Character analysis: speakers of "..., said Name." per chapter."""
    chapters = re.split(r"^=== \[(\d+)\] .* ===$", text, flags=re.M)
    found: dict[str, list[int]] = {}
    numbers = []
    for i in range(1, len(chapters) - 1, 2):
        n = int(chapters[i])
        numbers.append(n)
        for name in re.findall(r"said ([A-Z][a-z]+)", chapters[i + 1]):
            found.setdefault(name, [])
            if n not in found[name]:
                found[name].append(n)
    characters = [
        {"ref": "", "name": name, "aliases": [], "gender": "unknown", "age": "unknown",
         "role": "", "description": "", "speech_style": "", "importance": 3,
         "speaks": True, "chapters": chs}
        for name, chs in found.items()
    ]
    return json.dumps({
        "characters": characters,
        "chapters": [{"n": n, "narration": "third_person", "pov": ""} for n in numbers],
    })


def cast(text: str) -> str:
    """Voice casting: the first listed base voice for every character."""
    voice = re.search(r"^(\w{2}_\w+) \|", text, flags=re.M)
    refs = re.findall(r"^(c\d+) \|", text, flags=re.M)
    return json.dumps({"voices": [
        {"ref": r, "parts": [{"voice": voice[1] if voice else "af_heart", "weight": 1}],
         "speed": 1.0, "reason": "fake"}
        for r in refs
    ]})


def attribute(text: str) -> str:
    """Speaker attribution: the "said Name" of the quotation's paragraph."""
    ids = {name: "c" + n for n, name in re.findall(r"^c(\d+) \| ([^|]+?) \|", text, flags=re.M)}
    quotes = []
    for para in text.split("\n\n"):
        said = re.search(r"said ([A-Z][a-z]+)", para)
        for n in re.findall(r"\[q(\d+)\]", para):
            speaker = ids.get(said[1], "unknown") if said else "unknown"
            quotes.append({"q": int(n), "speaker": speaker, "emotion": "neutral",
                           "confidence": 0.9 if speaker != "unknown" else 0.2})
    return json.dumps({"quotes": quotes})


def prepare(text: str) -> str:
    """Chapter preparation: a pause after "said Anna.", a respelling of Anna."""
    out = {"moods": [], "pauses": [], "spoken": [], "pronunciations": []}
    for n, para in re.findall(r"^\[p(\d+)\] (.*)$", text, flags=re.M):
        if "said Anna." in para:
            out["pauses"].append({"p": int(n), "after": "said Anna.", "length": "long"})
            out["pronunciations"] = [{"term": "Anna", "say": "ah-nah", "note": "fake"}]
    return json.dumps(out)


def chat_answer(req: dict) -> str:
    """Answers the app's prompts (see the module docstring)."""
    for m in reversed(req.get("messages", [])):
        text = m.get("content") or ""
        add = re.search(r"Add (\d+) and (\d+)", text)
        word = re.search(r"exactly this word: (\w+)", text)
        if add and word:
            return json.dumps({"sum": int(add[1]) + int(add[2]), "echo": word[1]})
        if text.startswith("Pairs ("):
            return json.dumps({"decisions": []})
        if text.startswith("Base voices"):
            return cast(text)
        if "Quotations to attribute" in text:
            return attribute(text)
        if "Paragraphs to prepare" in text:
            return prepare(text)
        if "Chapters (" in text:
            return analyse(text)
    return "{}"


class Handler(BaseHTTPRequestHandler):
    def _send(self, status: int, body: bytes, content_type: str) -> None:
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _authorized(self) -> bool:
        if LLM_KEY and self.headers.get("Authorization") != f"Bearer {LLM_KEY}":
            self._send(401, b'{"error":{"message":"invalid API key"}}', "application/json")
            return False
        return True

    def do_GET(self) -> None:  # noqa: N802
        if self.path == "/v1/models":
            if self._authorized():
                body = json.dumps({"object": "list", "data": [{"id": m, "object": "model"} for m in MODELS]})
                self._send(200, body.encode(), "application/json")
        elif self.path == "/get_predefined_voices":
            self._send(200, json.dumps(CHATTERBOX_VOICES).encode(), "application/json")
        elif self.path == "/get_reference_files":
            self._send(200, json.dumps(CHATTERBOX_REFERENCES).encode(), "application/json")
        elif self.path == "/health":
            self._send(200, b'{"status":"healthy"}', "application/json")
        elif self.path.startswith("/v1/audio/voices"):
            voices = [{"id": v, "name": v, "overall_grade": "B"} for v in VOICES]
            body = json.dumps({"voices": voices, "default_voice": VOICES[0]}).encode()
            self._send(200, body, "application/json")
        else:
            self._send(404, b"not found", "text/plain")

    def do_POST(self) -> None:  # noqa: N802
        if self.path == "/v1/chat/completions":
            if not self._authorized():
                return
            req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
            content = chat_answer(req)
            body = {
                "id": "chatcmpl-fake",
                "object": "chat.completion",
                "model": req.get("model", ""),
                "choices": [{"index": 0, "finish_reason": "stop",
                             "message": {"role": "assistant", "content": content}}],
                "usage": {"prompt_tokens": 50, "completion_tokens": len(content) // 4,
                          "total_tokens": 50 + len(content) // 4},
            }
            self._send(200, json.dumps(body).encode(), "application/json")
            return
        if self.path == "/upload_reference":
            raw = self.rfile.read(int(self.headers.get("Content-Length", "0")))
            head = f"Content-Type: {self.headers.get('Content-Type', '')}\r\n\r\n".encode()
            uploaded = []
            for part in BytesParser(policy=HTTP).parsebytes(head + raw).iter_parts():
                name = part.get_filename()
                if name and name not in CHATTERBOX_REFERENCES:
                    CHATTERBOX_REFERENCES.append(name)
                    uploaded.append(name)
            self._send(200, json.dumps({"uploaded_files": uploaded, "errors": []}).encode(), "application/json")
            return
        if self.path == "/design":
            req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
            if not req.get("text") or not req.get("description"):
                self._send(400, json.dumps({"detail": "text and description are required"}).encode(), "application/json")
                return
            time.sleep(DESIGN_SECONDS)
            self._send(200, wav(tone(3)), "audio/wav")
            return
        if self.path == "/api/unload":
            self._send(200, json.dumps({"status": "unloaded"}).encode(), "application/json")
            return
        if self.path == "/restart_server":
            self._send(200, json.dumps({"message": "loaded", "restart_needed": False}).encode(), "application/json")
            return
        if self.path == "/tts":
            req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
            voice = req.get("predefined_voice_id") if req.get("voice_mode") == "predefined" else req.get("reference_audio_filename")
            known = [v["filename"] for v in CHATTERBOX_VOICES] + CHATTERBOX_REFERENCES
            if voice not in known or not req.get("text"):
                self._send(404, json.dumps({"detail": "voice not found"}).encode(), "application/json")
                return
            per_char = 0.02 * (0.5 + float(req.get("exaggeration", 0.5)))
            self._send(200, wav(tone(per_char * len(req["text"]))), "audio/wav")
            return
        if self.path not in ("/v1/audio/speech", "/dev/captioned_speech"):
            self._send(404, b"not found", "text/plain")
            return
        req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
        for part in req.get("voice", "").split("+"):
            if part.split("(")[0] not in VOICES:
                self._send(400, json.dumps({"detail": "unknown voice"}).encode(), "application/json")
                return
        text = req.get("input", "")
        per_char = 0.02 / float(req.get("speed", 1) or 1)
        pcm = tone(per_char * len(text))
        if self.path == "/dev/captioned_speech":
            # A word starts after the characters before it (as the tone).
            words = [
                {"word": m.group(), "start_time": round(per_char * m.start(), 3),
                 "end_time": round(per_char * m.end(), 3)}
                for m in re.finditer(r"\S+", text)
            ]
            body = {"audio": base64.b64encode(pcm).decode(), "audio_format": "audio/pcm", "timestamps": words}
            self._send(200, json.dumps(body).encode(), "application/json")
            return
        if req.get("response_format", "mp3") == "pcm":
            self._send(200, pcm, "audio/pcm")
        else:
            self._send(200, wav(pcm), "audio/wav")

    def log_message(self, *args) -> None:  # keep e2e logs readable
        pass


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8880), Handler).serve_forever()
