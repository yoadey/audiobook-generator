# Voice design server (Qwen3-TTS VoiceDesign on CPU) of audiobook-generator:
# speaks a sample in a voice described in words; the app clones the clip
# with Chatterbox. See docs/tts-engines.md. The model (~4 GB) is downloaded
# on first start into HF_HOME (a volume in the chart).
FROM python:3.12.13-slim-bookworm@sha256:4766d8b510c428e595d74b9cc5bbb2fae8e26316fffb4adc89908d79aacd58a2

ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 HF_HOME=/models

RUN apt-get update \
    && apt-get install -y --no-install-recommends libsndfile1 sox \
    && rm -rf /var/lib/apt/lists/*

# CPU builds of torch; qwen-tts pins its transformers and accelerate.
RUN python3 -m pip install --no-cache-dir --index-url https://download.pytorch.org/whl/cpu \
      torch==2.11.0 torchaudio==2.11.0 \
    && python3 -m pip install --no-cache-dir \
      qwen-tts==0.1.1 fastapi==0.142.2 uvicorn==0.53.0 soundfile==0.14.0

RUN useradd --uid 1000 --create-home app && mkdir -p /models && chown app /models
WORKDIR /app
COPY server.py .
USER 1000
EXPOSE 8000
CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "8000"]
