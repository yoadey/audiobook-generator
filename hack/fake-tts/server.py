"""Minimal stand-in for Kokoro-FastAPI used by the e2e test and local runs.

Implements GET /health, GET /v1/audio/voices (v0.9 response shape) and
POST /v1/audio/speech. Speech is a quiet 220 Hz tone whose length grows with
the text (20 ms per character), as 24 kHz 16-bit mono PCM; any other
response_format gets a WAV file.
"""

import json
import math
import struct
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

RATE = 24000
VOICES = ["af_heart", "af_bella", "am_michael", "bf_emma", "bm_george"]


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


class Handler(BaseHTTPRequestHandler):
    def _send(self, status: int, body: bytes, content_type: str) -> None:
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802
        if self.path == "/health":
            self._send(200, b'{"status":"healthy"}', "application/json")
        elif self.path.startswith("/v1/audio/voices"):
            voices = [{"id": v, "name": v, "overall_grade": "B"} for v in VOICES]
            body = json.dumps({"voices": voices, "default_voice": VOICES[0]}).encode()
            self._send(200, body, "application/json")
        else:
            self._send(404, b"not found", "text/plain")

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/v1/audio/speech":
            self._send(404, b"not found", "text/plain")
            return
        req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))
        for part in req.get("voice", "").split("+"):
            if part.split("(")[0] not in VOICES:
                self._send(400, json.dumps({"detail": "unknown voice"}).encode(), "application/json")
                return
        pcm = tone(0.02 * len(req.get("input", "")) / float(req.get("speed", 1) or 1))
        if req.get("response_format", "mp3") == "pcm":
            self._send(200, pcm, "audio/pcm")
        else:
            self._send(200, wav(pcm), "audio/wav")

    def log_message(self, *args) -> None:  # keep e2e logs readable
        pass


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8880), Handler).serve_forever()
