"""Teach Chatterbox-TTS-Server (devnen, sha-915ae28) to load Chatterbox Nano.

Nano is Turbo's architecture with a smaller T3 (ChatterboxTurboTTS with
nano=True). Every replacement must apply exactly once, so a changed
upstream file fails the image build instead of silently loading Turbo.

Usage: patch_nano.py <server engine.py> <chatterbox tts_turbo.py>
"""
import sys

engine = [
    (
        '    "resembleai/chatterbox-turbo": "turbo",\n',
        '    "resembleai/chatterbox-turbo": "turbo",\n'
        '    # Nano selectors (patched in: Turbo with nano=True)\n'
        '    "chatterbox-nano": "nano",\n'
        '    "nano": "nano",\n'
        '    "resembleai/chatterbox-nano": "nano",\n',
    ),
    (
        '    if model_type == "turbo":\n        if not TURBO_AVAILABLE:',
        '    if model_type == "nano":\n'
        '        if not TURBO_AVAILABLE:\n'
        '            raise ImportError("Chatterbox Nano needs ChatterboxTurboTTS in the chatterbox package.")\n'
        '        logger.info(f"Model selector \'{selector}\' resolved to Nano model (ChatterboxTurboTTS, nano=True)")\n'
        '        return ChatterboxTurboTTS, "nano"\n\n'
        '    if model_type == "turbo":\n        if not TURBO_AVAILABLE:',
    ),
    (
        '            chatterbox_model = model_class.from_pretrained(device=model_device)\n',
        '            if model_type == "nano":\n'
        '                chatterbox_model = model_class.from_pretrained(device=model_device, nano=True)\n'
        '                # Everything else treats Nano as Turbo (tags, conds cache).\n'
        '                model_type = "turbo"\n'
        '            else:\n'
        '                chatterbox_model = model_class.from_pretrained(device=model_device)\n',
    ),
]
# The image has numpy 2, where float32 * float64 scalar is float64 (NEP 50):
# the loudness-normalised reference clip turns float64 and the tokenizer's
# float32 mel filters refuse it. Cast back before conditioning.
turbo = [
    (
        "            s3gen_ref_wav = self.norm_loudness(s3gen_ref_wav, _sr)\n",
        "            s3gen_ref_wav = self.norm_loudness(s3gen_ref_wav, _sr)\n"
        "        s3gen_ref_wav = s3gen_ref_wav.astype(\"float32\")\n",
    ),
]


def apply(path, patches):
    src = open(path).read()
    for old, new in patches:
        n = src.count(old)
        if n != 1:
            sys.exit(f"patch_nano: {path}: expected 1 match, found {n}:\n{old}")
        src = src.replace(old, new)
    open(path, "w").write(src)
    print("patch_nano: applied", len(patches), "patches to", path)


apply(sys.argv[1], engine)
apply(sys.argv[2], turbo)
