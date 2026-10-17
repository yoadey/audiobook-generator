# Chatterbox-TTS-Server (devnen) with Chatterbox Nano: devnen's CPU image,
# upstream chatterbox (which has Nano) and a small patch that lets the
# server load it (model.repo_id: chatterbox-nano). See docs/tts-engines.md.
# Bump BASE and CHATTERBOX_REF together and by hand: patch_nano.py fails
# the build when the server's code no longer matches.
ARG BASE=ghcr.io/devnen/chatterbox-tts-server:sha-915ae28-cpu
FROM ${BASE}

ARG CHATTERBOX_REF=5de7a54aa4e5e2baadb0182dde554908b48b85c2
# --no-deps keeps the image's torch and the rest of its tested stack.
RUN python3 -m pip install --no-cache-dir --no-deps --force-reinstall \
      "chatterbox-tts @ git+https://github.com/resemble-ai/chatterbox.git@${CHATTERBOX_REF}"

COPY patch_nano.py /tmp/patch_nano.py
RUN python3 /tmp/patch_nano.py /app/engine.py \
      "$(python3 -c 'import sysconfig; print(sysconfig.get_paths()["purelib"])')/chatterbox/tts_turbo.py" \
    && rm /tmp/patch_nano.py
