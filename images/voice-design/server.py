"""Voice design server of audiobook-generator: speaks a text in a voice
described in words, with Qwen3-TTS VoiceDesign on CPU.

POST /design {"text", "description", "seed", "language"} answers a WAV
(16-bit mono, at most MAX_SECONDS: a reference clip for Chatterbox);
GET /health answers 200 once the model is loaded (503 before). One design
runs at a time. A model that fails to load ends the process, so the
kubelet starts it again (a broken download is resumed).
"""

import io
import logging
import os
import threading

import numpy as np
import soundfile as sf
import torch
from fastapi import FastAPI, HTTPException, Response
from pydantic import BaseModel, Field

MODEL = os.environ.get("MODEL", "Qwen/Qwen3-TTS-12Hz-1.7B-VoiceDesign")
REVISION = os.environ.get("MODEL_REVISION") or None
DTYPE = {"float32": torch.float32, "bfloat16": torch.bfloat16}[os.environ.get("DTYPE", "float32")]
THREADS = int(os.environ.get("THREADS", "0") or 0)
MAX_SECONDS = 29.0  # Chatterbox clones from clips of at most 30 s

log = logging.getLogger("voice-design")
logging.basicConfig(level=logging.INFO)
app = FastAPI(title="voice-design")
model = None
busy = threading.Lock()


def load():
    global model
    try:
        from huggingface_hub import snapshot_download
        from qwen_tts import Qwen3TTSModel

        if THREADS > 0:
            torch.set_num_threads(THREADS)
        # The whole repository at one revision: qwen-tts itself loads its
        # tokenizers from the default branch whatever revision it is given.
        path = MODEL if os.path.isdir(MODEL) else snapshot_download(MODEL, revision=REVISION)
        log.info("loading %s at %s (%s)", MODEL, REVISION or "main", DTYPE)
        model = Qwen3TTSModel.from_pretrained(path, device_map="cpu", dtype=DTYPE, attn_implementation="sdpa")
        log.info("model loaded")
    except Exception:
        log.exception("loading the model failed")
        os._exit(1)


threading.Thread(target=load, daemon=True).start()


class Design(BaseModel):
    text: str = Field(min_length=1, max_length=2000)
    description: str = Field(min_length=1, max_length=1000)
    seed: int = Field(default=0, ge=0, le=2**31 - 1)
    language: str = Field(default="English", max_length=40)


@app.get("/health")
def health():
    if model is None:
        raise HTTPException(503, "loading the model")
    return {"status": "ok", "model": MODEL}


@app.post("/design")
def design(req: Design):
    # A sync handler runs in a worker thread: /health stays answered.
    if model is None:
        raise HTTPException(503, "loading the model")
    with busy:
        torch.manual_seed(req.seed)
        wavs, sr = model.generate_voice_design(
            text=req.text, instruct=req.description, language=req.language, max_new_tokens=2048
        )
    audio = np.asarray(wavs[0], dtype=np.float32).reshape(-1)[: int(MAX_SECONDS * sr)]
    if audio.size == 0:
        raise HTTPException(500, "no audio generated")
    peak = float(np.max(np.abs(audio)))
    if peak > 0:
        audio = audio * min(1.0, 0.95 / peak)
    buf = io.BytesIO()
    sf.write(buf, audio, sr, format="WAV", subtype="PCM_16")
    return Response(buf.getvalue(), media_type="audio/wav")
