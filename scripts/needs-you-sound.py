#!/usr/bin/env python3
"""Makes apple/Resources/NeedsYou.caf, the sound for something that needs you:
a soft kalimba C4 with a quieter octave above, damped after ~0.4s.

    python3 scripts/needs-you-sound.py   (needs numpy; uses macOS's afconvert)
"""
import os, subprocess, tempfile, wave
import numpy as np

SR = 48000
rng = np.random.default_rng(1)

def kalimba(f, d=0.8):
    t = np.arange(int(SR * d)) / SR
    # A metal tine: strongly inharmonic overtones that die fast.
    s = sum(a * np.sin(2 * np.pi * f * r * t) * np.exp(-t / tau)
            for r, a, tau in [(1, 1, 0.5), (5.9, 0.12, 0.04), (16.7, 0.03, 0.015)])
    # The thumb: a couple of ms of softened noise.
    n = rng.standard_normal(len(t)) * np.exp(-t / 0.004)
    k = np.exp(-np.arange(20) / 4); s += 0.072 * np.convolve(n, k / k.sum(), 'same')
    onset = 0.5 - 0.5 * np.cos(np.pi * np.clip(t / 0.002, 0, 1))
    return s * onset

def damp(x, hold, fade):
    """A hand on the tine: a smooth, quick fade after `hold` seconds."""
    t = np.arange(len(x)) / SR
    g = np.where(t < hold, 1.0, np.exp(-((t - hold) / fade) ** 2 * 3))
    return (x * g)[:int((hold + fade * 1.2) * SR)]

x = damp(kalimba(261.63) * 0.9 + 0.2 * kalimba(523.25), 0.37, 0.29)
x = x / np.max(np.abs(x)) * 0.5

root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
with tempfile.TemporaryDirectory() as tmp:
    wav = os.path.join(tmp, 'NeedsYou.wav')
    with wave.open(wav, 'wb') as w:
        w.setnchannels(1); w.setsampwidth(2); w.setframerate(SR)
        w.writeframes((x * 32767).astype('<i2').tobytes())
    subprocess.run(['afconvert', '-f', 'caff', '-d', 'LEI16', wav,
                    os.path.join(root, 'apple/Resources/NeedsYou.caf')], check=True)
