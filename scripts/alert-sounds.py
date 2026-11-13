#!/usr/bin/env python3
"""Makes Hammock's own alert sounds, in apple/Resources (Settings ▸ Notifications
picks one for each kind of notification):

    NeedsYou.caf   Kalimba: a soft kalimba C4 with a quieter octave above, damped after ~0.4s
    Chime.caf      Chime: two kalimba notes rising, G4 then C5
    Low.caf        Low: two low kalimba notes falling, A3 then E3, damped sooner
    Marimba.caf    Marimba: a wooden bar's F4, short and round
    Bell.caf       Bell: a small, soft bell's A5 that rings on

    python3 scripts/alert-sounds.py   (needs numpy; uses macOS's afconvert)

NeedsYou.caf comes out the same as ever: the noise is seeded, and drawn
for it first.
"""
import os, subprocess, tempfile, wave
import numpy as np

SR = 48000
rng = np.random.default_rng(1)

def thumb(n, level):
    """A couple of ms of softened noise: what strikes the tine or bar."""
    t = np.arange(n) / SR
    x = rng.standard_normal(n) * np.exp(-t / 0.004)
    k = np.exp(-np.arange(20) / 4)
    return level * np.convolve(x, k / k.sum(), 'same')

def onset(n):
    t = np.arange(n) / SR
    return 0.5 - 0.5 * np.cos(np.pi * np.clip(t / 0.002, 0, 1))

def kalimba(f, d=0.8):
    t = np.arange(int(SR * d)) / SR
    # A metal tine: strongly inharmonic overtones that die fast.
    s = sum(a * np.sin(2 * np.pi * f * r * t) * np.exp(-t / tau)
            for r, a, tau in [(1, 1, 0.5), (5.9, 0.12, 0.04), (16.7, 0.03, 0.015)])
    s += thumb(len(t), 0.072)
    return s * onset(len(t))

def marimba(f, d=0.9):
    t = np.arange(int(SR * d)) / SR
    # A wooden bar tuned so its overtones sit at 4x and 10x, the higher ones gone in a blink.
    s = sum(a * np.sin(2 * np.pi * f * r * t) * np.exp(-t / tau)
            for r, a, tau in [(1, 1, 0.32), (4, 0.25, 0.05), (9.9, 0.05, 0.012)])
    s += thumb(len(t), 0.05)
    return s * onset(len(t))

def bell(f, d=1.6):
    t = np.arange(int(SR * d)) / SR
    # A small bell's partials (hum, prime, tierce, quint, nominal), the high ones shorter.
    s = sum(a * np.sin(2 * np.pi * f * r * t) * np.exp(-t / tau)
            for r, a, tau in [(0.5, 0.25, 0.9), (1, 1, 0.7), (1.19, 0.3, 0.45), (1.5, 0.2, 0.35), (2, 0.35, 0.25), (2.74, 0.1, 0.12)])
    s += thumb(len(t), 0.03)
    return s * onset(len(t))

def damp(x, hold, fade):
    """A hand on the tine: a smooth, quick fade after `hold` seconds."""
    t = np.arange(len(x)) / SR
    g = np.where(t < hold, 1.0, np.exp(-((t - hold) / fade) ** 2 * 3))
    return (x * g)[:int((hold + fade * 1.2) * SR)]

def after(x, y, gap):
    """y starting `gap` seconds into x, the two sounding together."""
    start = int(gap * SR)
    out = np.zeros(max(len(x), start + len(y)))
    out[:len(x)] += x
    out[start:start + len(y)] += y
    return out

def level(x, peak=0.5):
    return x / np.max(np.abs(x)) * peak

sounds = {
    # Drawn first, so it stays as it was.
    'NeedsYou': level(damp(kalimba(261.63) * 0.9 + 0.2 * kalimba(523.25), 0.37, 0.29)),
    'Chime': level(after(damp(kalimba(392.00) * 0.8, 0.14, 0.12),
                         damp(kalimba(523.25) + 0.15 * kalimba(1046.5), 0.4, 0.3), 0.13)),
    'Low': level(after(damp(kalimba(220.00), 0.12, 0.1),
                       damp(kalimba(164.81) + 0.15 * kalimba(329.63), 0.3, 0.25), 0.14), 0.55),
    'Marimba': level(damp(marimba(349.23) + 0.15 * marimba(698.46), 0.35, 0.25)),
    'Bell': level(damp(bell(880.00), 0.9, 0.5), 0.4),
}

root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
with tempfile.TemporaryDirectory() as tmp:
    for name, x in sounds.items():
        wav = os.path.join(tmp, name + '.wav')
        with wave.open(wav, 'wb') as w:
            w.setnchannels(1); w.setsampwidth(2); w.setframerate(SR)
            w.writeframes((x * 32767).astype('<i2').tobytes())
        subprocess.run(['afconvert', '-f', 'caff', '-d', 'LEI16', wav,
                        os.path.join(root, 'apple/Resources', name + '.caf')], check=True)
