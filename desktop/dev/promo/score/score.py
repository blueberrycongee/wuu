#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["numpy>=1.26", "scipy>=1.11", "soundfile>=0.12"]
# ///
"""Builds the promo soundtrack from real recordings only, then checks it.

Every sample in the mix is a slice of a recording listed in sources.json:
small objects (switches, a mouse, pens, keys, marbles, glass, paper) for the
cues, and real marimba, piano and plucked double-bass notes for the music.
There are no oscillators or noise generators; notes are pitched by
resampling a recorded note, and the master is gain and a peak limiter.

Each cue in ../cues.json gets its own recorded gesture, placed so that its
attack lands on the frame of the event. The build then measures its own
output: attack timing against every cue, whether each cue stands out of the
music, integrated loudness and true peak, and the silence of the hush.

    uv run desktop/dev/promo/score/score.py           # build and check
    uv run desktop/dev/promo/score/score.py --verify desktop/out-dev/promo/wuu-promo.mp4

`--verify` also needs ffmpeg: it measures the audio offset in the exported
video and finds its hard cuts from the frames.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import math
import re
import shutil
import subprocess
import sys
import urllib.parse
import urllib.request
from dataclasses import dataclass
from fractions import Fraction
from pathlib import Path

import numpy as np
import soundfile as sf
from scipy import signal
from scipy.ndimage import maximum_filter1d, minimum_filter1d

SR = 48_000
HERE = Path(__file__).resolve().parent
PROMO = HERE.parent
OUT = PROMO.parents[1] / "out-dev" / "promo"
CACHE = OUT / "sounds"
SOURCES = json.loads((HERE / "sources.json").read_text())
SCORE = json.loads((PROMO / "cues.json").read_text())
BEAT = 60 / SCORE["bpm"]
LENGTH = round(SCORE["beats"] * BEAT * SR)
FPS = 60

TARGET_LUFS = -16.0
CEILING_DBTP = -1.5


def sec(beat: float) -> float:
    return beat * BEAT


# ---------------------------------------------------------------------------
# Recordings


def fetch(url: str, name: str, sha256: str | None = None) -> Path:
    path = CACHE / name
    if not path.exists():
        path.parent.mkdir(parents=True, exist_ok=True)
        request = urllib.request.Request(url, headers={"User-Agent": "wuu-promo-score"})
        data = urllib.request.urlopen(request, timeout=120).read()
        partial = path.with_suffix(path.suffix + ".part")
        partial.write_bytes(data)
        partial.rename(path)
    if sha256 and hashlib.sha256(path.read_bytes()).hexdigest() != sha256:
        raise SystemExit(f"{name}: the download no longer matches sources.json; review the recording before updating its checksum")
    return path


_decoded: dict[Path, np.ndarray] = {}


def decode(path: Path) -> np.ndarray:
    """Stereo float64 at SR."""
    if path not in _decoded:
        data, rate = sf.read(path, dtype="float64", always_2d=True)
        data = np.repeat(data, 2, axis=1) if data.shape[1] == 1 else data[:, :2]
        if rate != SR:
            f = Fraction(SR, rate)
            data = signal.resample_poly(data, f.numerator, f.denominator, axis=0)
        _decoded[path] = data
    return _decoded[path]


FREESOUND = {s["id"]: s for s in SOURCES["freesound"]}


def recording(sound_id: int) -> np.ndarray:
    """A small-object recording, folded to mono: the cues are placed by pan,
    and slicing reads the same signal that is heard."""
    s = FREESOUND[sound_id]
    x = decode(fetch(s["preview"], f"freesound/{sound_id}.mp3", s["sha256"]))
    return np.repeat(x.mean(axis=1, keepdims=True), 2, axis=1)


def library_file(lib: str, path: str) -> np.ndarray:
    meta = SOURCES["libraries"][lib]
    url = f"https://raw.githubusercontent.com/{meta['repo']}/{meta['commit']}/{urllib.parse.quote(path)}"
    return decode(fetch(url, f"{lib}/{path}"))


# ---------------------------------------------------------------------------
# Gestures: one physical action in a recording (a click, a snip, a bounce train)

HOP = 48  # 1 ms envelope frames


def envelope_db(x: np.ndarray) -> np.ndarray:
    mono = np.abs(x).max(axis=1)
    n = len(mono) // HOP
    return 20 * np.log10(mono[: n * HOP].reshape(n, HOP).max(axis=1) + 1e-9)


def level(x: np.ndarray) -> np.ndarray:
    """Peak level over a centred 1 ms window, the envelope every attack is read from."""
    return maximum_filter1d(np.abs(x).max(axis=1), HOP)


def attack_in(env: np.ndarray, lo: int, hi: int) -> tuple[int, float]:
    """The perceived start of the loudest hit in [lo, hi): where the envelope
    rises through 30% of the way from what was sounding before to the hit's
    peak, at most 10 ms before that peak. Placement and the checks both use
    it, so a quiet pre-click never stands in for the body of the sound."""
    lo, hi = max(0, lo), min(len(env), hi)
    k = lo + int(np.argmax(env[lo:hi]))
    peak = float(env[k])
    back = max(0, k - int(0.01 * SR))
    base = float(env[back:k + 1].min())
    threshold = base + 0.3 * (peak - base)
    j = k
    while j > back and env[j - 1] >= threshold:
        j -= 1
    return j + HOP // 2, peak


LEAD_IN = 0.005  # a gesture's main hit is looked for from just before its first onset


@dataclass(frozen=True)
class Gesture:
    source: str
    start: int   # first sample of the slice, just before the first onset
    first: int   # the first onset, which may be a quiet pre-click
    attack: int  # the perceived start of the main hit: this sample lands on the cue
    end: int
    peak: float

    @property
    def key(self) -> tuple[str, int]:
        return (self.source, self.attack)


def onset_frames(db: np.ndarray, above: float, rise_db: float = 12.0) -> list[int]:
    """1 ms frames where the level jumps `rise_db` within 6 ms and is above
    `above` dBFS, at least 40 ms apart."""
    rises = db[6:] - minimum_filter1d(db, 6, origin=2)[5:-1]
    onsets: list[int] = []
    for i in np.nonzero((rises >= rise_db) & (db[6:] >= above))[0] + 6:
        if not onsets or i - onsets[-1] >= 40:
            onsets.append(int(i))
    return onsets


def gestures(name: str, x: np.ndarray, gap: float, max_len: float, window: float, hit: bool, rise_db: float = 12.0) -> list[Gesture]:
    """Split a recording into gestures: onsets closer than `gap` belong to one.

    The attack is the loudest hit within `window` of the first onset. For a
    `hit` the gesture also ends before anything louder than that hit, so the
    sound heard as the event is the one on the cue; textures (tears, tape
    pulls) keep their whole length and are placed by their first moments."""
    db = envelope_db(x)
    floor = float(np.percentile(db, 20))
    env = level(x)
    onsets = onset_frames(db, floor + 24, rise_db)
    groups: list[list[int]] = []
    for o in onsets:
        if groups and (o - groups[-1][-1]) * HOP < gap * SR:
            groups[-1].append(o)
        else:
            groups.append([o])
    out = []
    for k, g in enumerate(groups):
        first = g[0] * HOP
        nxt = groups[k + 1][0] * HOP - 2 * HOP if k + 1 < len(groups) else len(env)
        attack, hit_peak = attack_in(env, first - int(LEAD_IN * SR), min(nxt, first + int(window * SR)))
        start = max(0, min(first, attack) - int(0.003 * SR))
        # The gesture ends where it has decayed 40 dB, at the next gesture, or at max_len.
        limit = min(len(env), attack + int(max_len * SR), nxt)
        if hit:
            settle = attack + int(0.01 * SR)
            louder = np.nonzero(env[settle:limit] > 1.12 * hit_peak)[0]
            if len(louder):
                limit = settle + int(louder[0]) - int(0.002 * SR)
        peak = float(env[start:limit].max())
        tail = np.nonzero(db[(g[-1] + 5):(limit // HOP)] < 20 * math.log10(peak + 1e-9) - 40)[0]
        end = min(limit, (g[-1] + 5 + int(tail[0])) * HOP) if len(tail) else limit
        # Quiet before it starts: the previous sound has died away.
        pre = env[max(0, start - int(0.05 * SR)): max(0, start - int(0.002 * SR))]
        if len(pre) and pre.max() > 0.12 * peak:
            continue
        if end - attack < 0.012 * SR or peak < 10 ** ((floor + 30) / 20):
            continue
        out.append(Gesture(name, start, first, attack, end, peak))
    return out


def closing_level(x: np.ndarray, g: Gesture) -> float:
    """How loud a gesture's last 5 ms are against its loudest moment, in dB."""
    body = np.abs(x[g.start: g.end])
    return 20 * math.log10(body[-240:].max() / (body.max() + 1e-12) + 1e-12)


def tail_fade(x: np.ndarray, g: Gesture) -> float:
    """A gesture that decayed on its own needs only a short fade; one cut off
    while still sounding gets a long one, so it tails away instead of stopping."""
    after = (g.end - g.attack) / SR  # the fade never reaches back into the attack
    if closing_level(x, g) < -30:
        return min(0.012, after / 2)
    return min(after * 0.6, max(0.025, (g.end - g.start) / SR * 0.4))


def opening_strength(x: np.ndarray, g: Gesture) -> float:
    """How loud a gesture's first 30 ms are against its loudest moment, in dB."""
    head = np.abs(x[g.start: g.attack + int(TEXTURE_WINDOW * SR)]).max()
    return 20 * math.log10(head / (np.abs(x[g.start: g.end]).max() + 1e-12) + 1e-12)


def pitch_of(x: np.ndarray, g: Gesture, lo: float = 250.0, hi: float = 6000.0) -> float:
    """Frequency of the strongest partial in the first 150 ms of a pitched hit."""
    seg = x[g.attack + int(0.005 * SR): g.attack + int(0.155 * SR)].mean(axis=1)
    if len(seg) < 1024:
        return float("nan")
    n = 1 << 16
    spec = np.abs(np.fft.rfft(seg * np.hanning(len(seg)), n))
    freqs = np.fft.rfftfreq(n, 1 / SR)
    band = (freqs >= lo) & (freqs <= hi)
    return float(freqs[band][np.argmax(spec[band])])


def resample(x: np.ndarray, semitones: float) -> np.ndarray:
    """Play a recording faster or slower, which is how a sampler pitches a note."""
    if abs(semitones) < 1e-4:
        return x
    f = Fraction(2 ** (semitones / 12)).limit_denominator(600)
    return signal.resample_poly(x, f.denominator, f.numerator, axis=0)


def fade(clip: np.ndarray, fade_in: float, fade_out: float) -> np.ndarray:
    clip = clip.copy()
    a, b = int(fade_in * SR), int(fade_out * SR)
    if a:
        clip[:a] *= (0.5 - 0.5 * np.cos(np.linspace(0, np.pi, a)))[:, None]
    if b and len(clip) > b:
        clip[-b:] *= (0.5 + 0.5 * np.cos(np.linspace(0, np.pi, b)))[:, None]
    return clip


# ---------------------------------------------------------------------------
# Voices: which recording answers which kind of cue


HIT_WINDOW = 0.08      # a hit's loudest moment is looked for this long after its first onset
TEXTURE_WINDOW = 0.03  # a texture is placed by its first moments
NOTE_WINDOW = 0.012    # an instrument cue is read back from the start of its chord


@dataclass(frozen=True)
class Voice:
    source: int
    gap: float
    max_len: float
    level: float        # peak dBFS before mastering
    pan: float = 0.0
    tuned: bool = False  # pitch each hit to the harmony
    min_len: float = 0.0
    hit: bool = True     # False for textures such as tears and tape pulls

    @property
    def window(self) -> float:
        return HIT_WINDOW if self.hit else TEXTURE_WINDOW


V = Voice
VOICES: dict[str, Voice] = {
    # Clicks: each switch or button is its own recording.
    "lamp-on": V(556318, 0.05, 0.35, -9, 0.3),
    "lamp-off": V(499771, 0.05, 0.3, -10, 0.2),
    "breaker": V(637863, 0.06, 0.5, -7),
    "mouse": V(335170, 0.05, 0.22, -11),
    "pen": V(197877, 0.08, 0.25, -12, -0.25),
    "button": V(499771, 0.03, 0.12, -9, -0.2),
    "wall-switch": V(595832, 0.05, 0.3, -10, 0.1),
    # Paper for cuts, scissors, tape and stamps. The page turns that mark the
    # splits have to be gone before a click a sixteenth later.
    "page": V(318615, 0.08, 0.12, -12),
    "page-long": V(318615, 0.3, 1.2, -12, min_len=0.3, hit=False),
    "snip": V(707812, 0.05, 0.3, -11, 0.15),
    "snip-2": V(788313, 0.05, 0.3, -11),
    "tear": V(634324, 0.12, 0.5, -8, min_len=0.25, hit=False),
    "tape": V(390166, 0.05, 0.22, -11),
    "tape-pull": V(390166, 0.18, 1.2, -12, min_len=0.4, hit=False),
    "tape-rip": V(747203, 0.1, 0.6, -11),
    "stamp": V(448474, 0.12, 0.4, -10),
    "stamp-firm": V(362624, 0.1, 0.35, -9),
    "stamp-heavy": V(470710, 0.1, 0.6, -8),
    "phone-book": V(451652, 0.12, 0.35, -7),
    "book": V(836521, 0.15, 0.5, -6),
    "stapler": V(546368, 0.14, 0.5, -9),
    "deadbolt": V(635601, 0.18, 0.8, -11),
    # Marbles fall and bounce. The hush is one marble and its bounces, which the
    # film draws at the times in cues.json.
    "marble-drop": V(628995, 0.35, 1.08, -11, -0.1, min_len=0.4),
    "marble-hit": V(628995, 0.06, 0.45, -12, 0.1),
    "drops": V(202546, 0.06, 0.5, -12),
    "hover": V(245402, 0.03, 0.12, -22, -0.2),
    # One small object per harness: it calls in that voice, works in it, and signs off in it.
    "claude": V(206066, 0.08, 0.8, -13, -0.35, tuned=True),
    "codex": V(616835, 0.06, 0.28, -14, 0.35, hit=False),
    "cursor": V(628997, 0.06, 0.4, -13, -0.55),
    "opencode": V(679983, 0.08, 0.6, -13, 0.5, tuned=True),
    "pi": V(51038, 0.06, 0.5, -13, 0.6, tuned=True),
    "devin": V(850456, 0.05, 0.4, -14, -0.6),
    "grok": V(806416, 0.06, 0.6, -13, 0.1),
    "hermes": V(245402, 0.04, 0.2, -14, 0.45),
    "idea": V(206066, 0.08, 1.2, -11, 0.2, tuned=True),
}

# Every sounding cue and the voice it speaks in. Instrument-only cues (the
# greeting notes) are played by the arrangement below instead.
AGENTS = ["claude", "codex", "cursor", "opencode", "pi", "devin", "grok", "hermes"]
CUE_VOICES: dict[str, str] = {
    "lamp-on": "lamp-on", "lights": "breaker", "answer": "mouse", "split": "page", "hush": "marble-drop",
    "idea": "idea", "test-snip": "snip", "scissors": "snip", "snip": "snip", "peel": "tape-rip", "app": "phone-book",
    "paste": "tape", "select": "pen", "plus": "button", "pill": "button", "hover": "hover", "pick": "button",
    "send": "wall-switch", "pi-arrives": "pi", "job": "tape-pull", "thump": "book", "fold": "page",
    "doll-snip": "snip-2", "unfold": "page", "tear": "tear", "hop": "drops", "stuck": "deadbolt", "leap": "tape-rip",
    "hand": "page", "aha": "deadbolt", "assemble": "page-long", "lock": "stapler", "letter": "tape", "lamp-off": "lamp-off",
    **{f"call.{a}": a for a in AGENTS}, **{f"evolve.{a}": a for a in AGENTS}, **{f"work.{a}": a for a in AGENTS},
    **{f"check.{a}": "stamp" for a in AGENTS}, **{f"drop.{a}": "marble-hit" for a in AGENTS},
    # The last two stamps are the firmest: the helped panel gets the heaviest.
    "check.cursor": "stamp-firm", "check.claude": "stamp-heavy",
}
# Cues the arrangement plays on instruments (Wuu's greeting notes and the last
# chord), and the one cue that is a swell rather than a hit.
PLAYED = {"wake", "hello", "marks"}
SWELL = "overload"
# Each check also answers in the harness's own voice.
ECHO = {f"check.{a}": a for a in AGENTS}

# ---------------------------------------------------------------------------
# Harmony and arrangement (F major, 112.5 BPM, 30 bars)

NOTE = {"C": 0, "C#": 1, "Db": 1, "D": 2, "D#": 3, "Eb": 3, "E": 4, "F": 5, "F#": 6, "Gb": 6, "G": 7, "G#": 8, "Ab": 8, "A": 9, "A#": 10, "Bb": 10, "B": 11}


def midi(name: str) -> int:
    m = re.fullmatch(r"([A-G][#b]?)(-?\d)", name)
    assert m, name
    return NOTE[m.group(1)] + 12 * (int(m.group(2)) + 1)


CHORD_TONES = {
    "F": "F A C", "Dm": "D F A", "Bb": "Bb D F", "C": "C E G", "C7": "C E G Bb", "Gm": "G Bb D", "Gm7": "G Bb D F",
    "Am": "A C E", "Db": "Db F Ab",
}
# Bar (from 1) -> chord, or two chords for the halves of the bar.
HARMONY = {
    2: "F", 3: "F", 4: "Dm", 5: "Bb", 6: "C", 7: ("Dm", "Bb"), 8: ("Gm", "C7"), 9: "F", 10: "F", 11: "Am", 12: "Bb",
    13: "C", 14: "Dm", 15: ("Gm7", "C7"), 16: "Db", 17: "Dm", 18: ("Bb", "C"), 19: "Dm", 20: ("Bb", "C"), 21: "F",
    22: "F", 23: "Dm", 24: "Bb", 25: "C7", 26: "F", 27: ("Bb", "C"), 28: "F", 29: "F", 30: "F",
}


def chord_at(beat: float) -> list[int]:
    bar = int(beat // 4) + 1
    chord = HARMONY.get(bar, "F")
    if isinstance(chord, tuple):
        chord = chord[0] if beat % 4 < 2 else chord[1]
    return [NOTE[n] for n in CHORD_TONES[chord].split()]


# (beat, note, velocity 0..1, length in beats)
BASS = [
    (4, "F2", 0.5, 2), (6, "C2", 0.8, 1), (7, "E2", 0.7, 1),
    (8, "F2", 0.8, 2), (10, "C3", 0.7, 1), (11, "A2", 0.7, 1),
    (12, "D2", 0.8, 2), (14, "A2", 0.7, 1), (15, "C3", 0.7, 1),
    (16, "Bb1", 0.85, 2), (18, "F2", 0.7, 1), (19, "D2", 0.7, 1),
    # The eighth-note drive of the split bar starts half a bar early, so the cut
    # lands inside a groove that is already running.
    (20, "C2", 0.85, 1), (21, "C2", 0.7, 1), (22, "G2", 0.72, 0.5), (22.5, "G2", 0.66, 0.5), (23, "E2", 0.74, 0.5), (23.5, "E2", 0.7, 0.5),
    *[(24 + k * 0.5, n, 0.75, 0.5) for k, n in enumerate(["D2", "D2", "A2", "A2", "Bb1", "Bb1", "F2", "F2"])],
    *[(28 + k * 0.5, n, 0.8 + k * 0.02, 0.5) for k, n in enumerate(["G1", "G1", "A1", "A1", "Bb1", "B1", "C2", "C#2"])],
    (36, "F2", 0.85, 1.5), (37.5, "C3", 0.6, 0.5), (38, "F2", 0.75, 1), (39, "A2", 0.7, 1),
    (40, "A1", 0.85, 1.5), (41.5, "E2", 0.6, 0.5), (42, "A1", 0.75, 1), (43, "C2", 0.7, 1),
    (44, "Bb1", 0.95, 1.5), (45.5, "F2", 0.6, 0.5), (46, "Bb1", 0.75, 1), (47, "D2", 0.7, 1),
    (48, "C2", 0.85, 1.5), (49.5, "G2", 0.6, 0.5), (50, "C2", 0.75, 1), (51, "E2", 0.7, 1),
    (52, "D2", 0.8, 1), (53, "A2", 0.7, 1), (54, "F2", 0.7, 1), (55, "A2", 0.7, 1),
    (56, "G1", 0.8, 1), (57, "Bb1", 0.7, 1), (58, "C2", 0.8, 1), (59, "C2", 0.7, 1),
    (61, "C#2", 1.0, 3),
    (64, "D2", 0.8, 2), (66, "A1", 0.7, 1), (67, "D2", 0.7, 1),
    (68, "Bb1", 0.8, 1), (69, "F2", 0.7, 1), (70, "C2", 0.8, 1), (71, "G2", 0.7, 1),
    (72, "D2", 0.75, 1), (73, "D2", 0.7, 1), (74, "D2", 0.72, 1), (75, "A1", 0.75, 1),
    (76, "Bb1", 0.8, 1), (77, "Bb1", 0.7, 1), (78, "C2", 0.8, 1), (79, "C2", 0.7, 1),
    (80, "F2", 0.8, 1), (81, "C2", 0.7, 1), (82, "F2", 0.7, 1), (83, "A2", 0.7, 1),
    (84, "F2", 0.85, 1.5), (85.5, "F2", 0.6, 0.5), (86, "C3", 0.7, 1), (87, "A2", 0.7, 1),
    (88, "D2", 0.7, 2), (90, "A1", 0.8, 1), (91, "D2", 0.8, 1),
    (92, "Bb1", 0.9, 1.5), (93.5, "F2", 0.6, 0.5), (94, "Bb1", 0.75, 1), (95, "D2", 0.7, 1),
    (96, "C2", 0.85, 1), (97, "G2", 0.7, 1), (98, "A#2", 0.7, 1), (99, "E2", 0.75, 1),
    (100, "F2", 0.95, 1), (101, "F2", 0.9, 1), (102, "C2", 0.75, 1), (103, "A2", 0.7, 1),
    (104, "Bb1", 0.8, 1), (105, "F2", 0.7, 1), (106, "C2", 0.8, 1), (107, "G2", 0.75, 1),
    (108, "F2", 0.8, 2), (110, "C3", 0.6, 1), (111, "A2", 0.6, 1),
    (112, "F1", 0.95, 6),
]

MARIMBA = [
    (2, "C5", 0.35, 2), (3, "F5", 0.55, 1), (3.5, "A5", 0.6, 1), (4, "C6", 0.65, 2),
    *[(16 + k * 0.5, n, 0.35, 0.5) for k, n in enumerate("D5 F5 Bb5 F5 D5 F5 Bb5 F5 E5 G5 C6 G5 E5 G5 C6 G5".split())],
    # The marimba keeps its eighths through the splits, so the pulse carries
    # across the cut; the notes under each split are soft and the "and" leads.
    *[(24 + k * 0.5, n, 0.42 if k % 2 else 0.2, 0.5) for k, n in enumerate("F5 A5 D6 A5 F5 Bb5 D6 Bb5".split())],
    *[(28 + k * 0.5, n, 0.4, 0.5) for k, n in enumerate("G5 Bb5 D6 Bb5 E5 G5 Bb5 C6".split())],
    *[(36.5 + k * 0.5, n, 0.3, 0.5) for k, n in enumerate(
        "C5 F5 C5 A4 C5 F5 C5 A4 C5 E5 C5 A4 C5 E5 C5 Bb4 D5 F5 D5 Bb4 D5 F5 D5 G4 C5 E5 C5 G4 C5 E5 C5".split())],
    (58.75, "C6", 0.5, 0.25), (59, "F6", 0.6, 1),
    *[(67 + k * 0.5, n, 0.45 + k * 0.03, 0.5) for k, n in enumerate("F4 G4 A4 Bb4 C5 D5 E5 F5".split())],
    (71, "A5", 0.5, 1), (71, "F5", 0.45, 1),
    *[(76 + k * 0.5, n, 0.5, 0.5) for k, n in enumerate("C5 F5 A5 C6 D5 G5 Bb5 D6".split())],
    *[(84 + k * 0.5, n, 0.3, 0.5) for k, n in enumerate("A4 C5 F5 C5 A4 C5 F5 C5".split())],
    *[(92 + k * 0.5, n, 0.32, 0.5) for k, n in enumerate("Bb4 D5 F5 D5 Bb4 D5 F5 D5".split())],
    *[(96 + k * 0.5, n, 0.5 + k * 0.03, 0.5) for k, n in enumerate("C5 E5 G5 Bb5 C6 E6 G6 Bb6".split())],
    (100, "F6", 0.75, 1),
    *[(102 + k * 0.5, n, 0.5, 0.5) for k, n in enumerate("F6 C6 A5 F5 D6 Bb5 G5 E5".split())],
    (110, "F5", 0.6, 0.5), (110.5, "A5", 0.65, 0.5), (111, "C6", 0.7, 1),
]
# A roll on the last chord: repeated real strokes, getting quieter.
MARIMBA += [(112 + k * 0.125, n, 0.55 * (1 - k / 40), 0.25) for k in range(32) for n in (("F5", "C6")[k % 2],)]

PIANO_CHORDS = [
    (4, "F3 A3 C4 F4", 0.35, 2), (6.5, "C4 F4 A4", 0.55, 1),
    (8, "F3 A3 C4", 0.35, 2), (12, "D3 F3 A3", 0.35, 2), (16, "Bb2 D3 F3", 0.38, 2), (20, "C3 E3 G3", 0.4, 2),
    (24.5, "D3 F3 A3", 0.42, 1), (26.5, "Bb2 D3 F3", 0.42, 1), (28, "G2 Bb2 D3", 0.45, 1), (30, "C3 E3 Bb3", 0.5, 1.5),
    (34, "F4 A4", 0.3, 1.5),
    (36.5, "F3 A3 C4", 0.35, 1.5), (40, "A2 C3 E3", 0.35, 2), (44, "Bb2 D3 F3", 0.45, 2), (48, "C3 E3 G3", 0.38, 2),
    (52.5, "D3 F3 A3", 0.32, 1.5), (56.75, "G2 Bb2 F3", 0.35, 1.25), (58.25, "C3 E3 Bb3", 0.35, 0.75),
    (61, "C#1 C#2", 0.95, 3), (61, "C#3 F3 G#3", 0.6, 3),
    (64, "D3 F3 A3", 0.3, 2), (68, "Bb2 D3 F3", 0.32, 2), (70, "C3 E3 G3", 0.34, 2),
    # Each tear is answered by a short stab on its "and".
    *[(b + 0.5, "D3 F3 A3", 0.38, 0.3) for b in (72, 73, 74, 75)],
    (76, "Bb2 D3 F3", 0.35, 2), (78, "C3 E3 G3", 0.38, 2), (80, "F3 A3 C4 F4", 0.38, 2),
    (84, "F3 A3 C4", 0.33, 2), (88, "D3 F3 A3", 0.28, 4), (92, "Bb2 D3 F3", 0.4, 2), (94, "Bb2 D3 F3", 0.3, 2),
    (96, "C3 E3 Bb3", 0.4, 4), (100, "F2 F3 A3 C4 F4", 0.6, 2), (104, "Bb2 D3 F3", 0.38, 2), (106, "C3 E3 G3", 0.42, 2),
    (108, "F3 A3 C4", 0.4, 2), (112, "F2 C3 F3 A3 C4 F4 A4 C5", 0.62, 6),
]
PIANO_MELODY = [
    (36.5, "C5", 0.5, 0.5), (37, "F5", 0.52, 1), (38, "A5", 0.55, 0.5), (38.5, "G5", 0.48, 0.5), (39, "F5", 0.5, 1),
    (40, "E5", 0.5, 1), (41, "C5", 0.45, 0.5), (41.5, "E5", 0.48, 0.5), (42, "A5", 0.55, 1), (43, "G5", 0.5, 1),
    (44, "F5", 0.52, 1), (45, "D5", 0.45, 0.5), (45.5, "F5", 0.48, 0.5), (46, "Bb5", 0.58, 1), (47, "A5", 0.5, 0.5), (47.5, "G5", 0.48, 0.5),
    (48, "G5", 0.55, 2), (50, "E5", 0.48, 1), (51, "C5", 0.45, 1),
    # Each session click is answered by the next note, on the "and".
    (52.5, "D5", 0.45, 0.5), (53.5, "F5", 0.48, 0.5), (54.5, "A5", 0.5, 0.5), (55.5, "D6", 0.55, 0.5),
    (84, "A5", 0.52, 1), (85, "C6", 0.55, 1), (86, "A5", 0.5, 0.5), (86.5, "G5", 0.48, 0.5), (87, "F5", 0.5, 1),
    (88, "D5", 0.4, 2),
    (92, "F5", 0.55, 1), (93, "Bb5", 0.58, 1), (94, "D6", 0.6, 0.5), (94.5, "C6", 0.55, 0.5), (95, "Bb5", 0.52, 0.5), (95.5, "A5", 0.5, 0.5),
    (100, "C6", 0.55, 1), (101, "F6", 0.6, 1),
    (110, "F5", 0.55, 0.5), (110.5, "A5", 0.58, 0.5), (111, "C6", 0.6, 1),
]
PIANO = [(b, n, v, d) for b, notes, v, d in PIANO_CHORDS for n in notes.split()] + PIANO_MELODY

# Everything stops dead on these cuts, so the next shot starts in silence.
CHOKES = [sec(b) for c in ("hush", "job", "lamp-off") for b in SCORE["cues"][c]["at"]]


# ---------------------------------------------------------------------------
# Instruments: recorded notes, each pitched from its nearest sampled neighbour


class Instrument:
    def __init__(self, lib: str, notes: dict[int, list[str]], octave_offset: int, level: float, pan: float, max_len: float,
                 release: float, layered: bool):
        # notes: nominal MIDI note -> files, either velocity layers (soft to loud) or round robins.
        self.lib, self.level, self.pan, self.max_len, self.release, self.layered = lib, level, pan, max_len, release, layered
        self.notes = {n + octave_offset: paths for n, paths in notes.items()}

    def _load(self, path: str) -> tuple[np.ndarray, int]:
        # Both libraries are tuned (measured within about 15 cents), so a note
        # is pitched from its file's nominal name.
        x = library_file(self.lib, path)
        env = level(x)
        first = int(np.argmax(env > 0.05 * env.max()))
        attack, _ = attack_in(env, first - int(LEAD_IN * SR), first + int(HIT_WINDOW * SR))
        return x, attack

    def note(self, name: str, velocity: float, length: float, beat: float) -> tuple[np.ndarray, int]:
        target = midi(name)
        nominal = min(self.notes, key=lambda n: (abs(n - target), n))
        paths = self.notes[nominal]
        # Soft notes use the softer layer. Round robins alternate by eighth-note
        # position, so repeated eighths differ and an edit elsewhere changes nothing.
        if self.layered:
            path = paths[min(len(paths) - 1, int(velocity * len(paths)))]
        else:
            path = paths[round(beat * 2) % len(paths)]
        x, attack = self._load(path)
        shift = target - nominal
        hold = min(int((length * BEAT + self.release) * SR), int(self.max_len * SR))
        # Cut the recording to the note's length first; resampling then scales it.
        span = int(hold * 2 ** (shift / 12)) + 64
        clip = resample(x[max(0, attack - int(0.002 * SR)): attack + span], shift)[:hold]
        clip = clip / (np.abs(clip).max() + 1e-12)
        clip = fade(clip, 0.001, min(self.release, len(clip) / SR / 2))
        gain = 10 ** (self.level / 20) * velocity ** 1.4
        return clip * gain, round(0.002 * SR / 2 ** (shift / 12))


def names(spec: str) -> list[str]:
    return spec.split()


MARIMBA_NOTES = {midi(n): [f"Idiophones/Struck Idiophones/Marimba/Marimba_hit_Outrigger_{n}_{d}_01.wav" for d in ("soft", "med", "loud")]
                 for n in names("F1 C2 G2 B2 F3 C4 G4 B4 F5 C6")}
PIANO_NOTES = {midi(n): [f"Chordophones/Zithers/Grand Piano, Steinway B/NoSus/JHPiano_NoSus_Close_{n}_vl{v}_rr1.wav" for v in (2, 3)]
               for n in names("C1 D1 E1 F#1 G#1 A#1 C2 D2 E2 F#2 G#2 A#2 C3 D3 E3 F#3 G#3 A#3 C4 D4 E4 F#4 G#4 A#4 C5 D5 E5 F#5 G#5 A#5 C6 D6 E6 F#6 G#6 A#6 C7")}
BASS_NOTES = {midi(n): [f"Strings/Solo Contrabass/Pizz/BKCtbss_Pizz_{n}_v1_rr{r}.wav" for r in (1, 2)]
              for n in names("E0 F#0 G0 A#0 C1 D1 E1 F#1 G#1 A1 C#2 E2 G#2 B2")}

# The sample libraries name notes an octave low for marimba and double bass.
MARIMBA_I = Instrument("vcsl", MARIMBA_NOTES, 12, -18, 0.15, 2.5, 0.08, layered=True)
PIANO_I = Instrument("vcsl", PIANO_NOTES, 0, -18.5, -0.1, 6.0, 0.25, layered=True)
BASS_I = Instrument("vsco", BASS_NOTES, 12, -15, 0.0, 2.5, 0.06, layered=False)


# ---------------------------------------------------------------------------
# Mixing


def pan_gains(pan: float) -> np.ndarray:
    a = (pan + 1) * math.pi / 4
    return np.array([math.cos(a), math.sin(a)]) * math.sqrt(2)


def add(bus: np.ndarray, clip: np.ndarray, at: float, attack: int, pan: float = 0.0, mono: bool = False) -> None:
    if mono:
        clip = np.repeat(clip.mean(axis=1, keepdims=True), 2, axis=1)
    clip = clip * pan_gains(pan)
    start = round(at * SR) - attack
    # A cut silences whatever was ringing when it lands.
    for choke in CHOKES:
        stop = round(choke * SR) - int(0.004 * SR)
        if at < choke and start + len(clip) > stop:
            clip = fade(clip[: max(1, stop - start)], 0, 0.006)
    lo, hi = max(0, start), min(len(bus), start + len(clip))
    if hi > lo:
        bus[lo:hi] += clip[lo - start: hi - start]


@dataclass
class Placed:
    cue: str
    kind: str
    at: float
    voice: str
    gesture: Gesture
    shift: float

    def offset(self, samples: int) -> int:
        """A distance within the recording, after pitching it."""
        return round(samples / 2 ** (self.shift / 12))


def nearest_chord_tone(freq: float, beat: float) -> float:
    """Semitones that move a pitched hit onto the nearest tone of the current chord."""
    m = 69 + 12 * math.log2(freq / 440)
    tones = chord_at(beat)
    best = min((m - (round(m) + d) for d in range(-3, 4) if (round(m) + d) % 12 in tones), key=abs)
    return -best


def plan() -> list[Placed]:
    """Give every cue its own gesture. Clicks and cuts never share a recording
    slice; work taps may repeat within a voice, like a drummer's stick."""
    pools: dict[str, list[Gesture]] = {}
    for name, v in VOICES.items():
        g = gestures(str(v.source), recording(v.source), v.gap, v.max_len, v.window, v.hit)
        pools[name] = [x for x in g if (x.end - x.attack) / SR >= v.min_len]
    used: set[tuple[str, int]] = set()
    events = sorted(((b, cid, c["kind"]) for cid, c in SCORE["cues"].items() for b in c["at"]
                     if cid not in PLAYED and cid != SWELL), key=lambda e: (e[0], e[1]))
    placed: list[Placed] = []
    work = [e for e in events if e[1].startswith("work.")]
    for b, cid, kind in [e for e in events if not e[1].startswith("work.")] + work:
        names_ = [CUE_VOICES[cid]] + ([ECHO[cid]] if cid in ECHO else [])
        for vname in names_:
            pool = pools[vname]
            fresh = [g for g in pool if g.key not in used]
            if not fresh and not cid.startswith("work."):
                raise SystemExit(f"{vname}: not enough distinct gestures for {cid} at beat {b}")
            x = recording(VOICES[vname].source)
            if kind == "cut" and not VOICES[vname].hit:
                # A texture that marks a cut has to start strongly, not fade in.
                fresh = sorted(fresh, key=lambda g: -opening_strength(x, g))
            elif kind == "cut" and cid != "hush":
                # A hit that marks a cut should end on its own, not be chopped. (The
                # hush keeps its gesture: the film draws that marble's bounces.)
                fresh = sorted(fresh, key=lambda g: closing_level(x, g))
            if fresh:
                g = fresh[0]
            else:
                g = pool[sum(1 for p in placed if p.cue == cid) % len(pool)]
            used.add(g.key)
            v = VOICES[vname]
            shift = nearest_chord_tone(pitch_of(recording(v.source), g), b) if v.tuned else 0.0
            placed.append(Placed(cid, kind, sec(b), vname, g, shift))
    return placed


def render_sfx(placed: list[Placed]) -> np.ndarray:
    bus = np.zeros((LENGTH, 2))
    for p in placed:
        v = VOICES[p.voice]
        x = recording(v.source)
        clip = fade(x[p.gesture.start: p.gesture.end], 0.0015, tail_fade(x, p.gesture))
        clip = resample(clip, p.shift)
        clip = clip / (np.abs(clip).max() + 1e-12) * 10 ** (v.level / 20)
        if p.cue in ECHO:
            clip *= 0.7
        add(bus, clip, p.at, p.offset(p.gesture.attack - p.gesture.start), v.pan, mono=True)
    return bus


def render_swell() -> np.ndarray:
    """The overload bar: one long shake of the key ring, swelling to the hush,
    taken from the densest bar of the jingle recording. It is its own stem so
    the cues inside that bar can still be read back."""
    bus = np.zeros((LENGTH, 2))
    keys = recording(616835)
    win = int(sec(4) * SR)
    csum = np.concatenate([[0.0], np.cumsum((keys ** 2).sum(axis=1))])
    start = int(np.argmax(csum[win:] - csum[:-win]))
    swell = keys[start: start + win] * np.linspace(0.05, 1.0, win)[:, None] ** 1.6
    add(bus, fade(swell / np.abs(swell).max() * 10 ** (-15 / 20), 0.01, 0.0), sec(SCORE["cues"][SWELL]["at"][0]), 0)
    return bus


def render_music() -> np.ndarray:
    bus = np.zeros((LENGTH, 2))
    for inst, notes in ((BASS_I, BASS), (MARIMBA_I, MARIMBA), (PIANO_I, PIANO)):
        for b, name, vel, length in notes:
            clip, attack = inst.note(name, vel, length, b)
            add(bus, clip, sec(b), attack, inst.pan)
    return bus


def duck(music: np.ndarray, times: list[float], depth_db: float = -4.0) -> np.ndarray:
    """Dip the music for a moment under every cut and click, so each one reads."""
    down, hold, up = int(0.004 * SR), int(0.03 * SR), int(0.06 * SR)
    floor = 10 ** (depth_db / 20)
    shape = np.concatenate([
        1 - (1 - floor) * (0.5 - 0.5 * np.cos(np.linspace(0, np.pi, down))),
        np.full(hold, floor),
        floor + (1 - floor) * (0.5 - 0.5 * np.cos(np.linspace(0, np.pi, up))),
    ])
    gain = np.ones(len(music))
    for t in times:
        a = round(t * SR) - down
        lo, hi = max(0, a), min(len(gain), a + len(shape))
        gain[lo:hi] = np.minimum(gain[lo:hi], shape[lo - a: hi - a])
    return music * gain[:, None]


# ---------------------------------------------------------------------------
# Loudness (ITU-R BS.1770-4 / EBU R 128)

K_SHELF = ([1.53512485958697, -2.69169618940638, 1.19839281085285], [1.0, -1.69065929318241, 0.73248077421585])
K_HIGHPASS = ([1.0, -2.0, 1.0], [1.0, -1.99004745483398, 0.99007225036621])


def k_weight(x: np.ndarray) -> np.ndarray:
    return signal.lfilter(*K_HIGHPASS, signal.lfilter(*K_SHELF, x, axis=0), axis=0)


def block_loudness(z: np.ndarray, size: float, step: float) -> np.ndarray:
    n, s = int(size * SR), int(step * SR)
    power = (z ** 2).sum(axis=1)
    csum = np.concatenate([[0.0], np.cumsum(power)])
    starts = np.arange(0, len(power) - n + 1, s)
    return -0.691 + 10 * np.log10((csum[starts + n] - csum[starts]) / n + 1e-15)


def loudness(x: np.ndarray) -> dict[str, float]:
    z = k_weight(x)
    m = block_loudness(z, 0.4, 0.1)
    gated = m[m > -70]
    rel = -0.691 + 10 * math.log10(np.mean(10 ** ((gated + 0.691) / 10))) - 10
    integrated = -0.691 + 10 * math.log10(np.mean(10 ** ((gated[gated > rel] + 0.691) / 10)))
    st = block_loudness(z, 3.0, 0.1)
    st_gated = st[st > -70]
    st_rel = st_gated[st_gated > -0.691 + 10 * math.log10(np.mean(10 ** ((st_gated + 0.691) / 10))) - 20]
    lra = float(np.percentile(st_rel, 95) - np.percentile(st_rel, 10))
    return {"integrated_lufs": integrated, "true_peak_dbtp": true_peak(x), "lra_lu": lra,
            "max_momentary_lufs": float(m.max()), "max_short_term_lufs": float(st.max())}


def true_peak(x: np.ndarray) -> float:
    return 20 * math.log10(np.abs(signal.resample_poly(x, 4, 1, axis=0)).max() + 1e-12)


def limit(x: np.ndarray, ceiling_db: float) -> np.ndarray:
    """Look-ahead peak limiter on the 4x oversampled signal, with a 60 ms release."""
    up = np.abs(signal.resample_poly(x, 4, 1, axis=0)).max(axis=1)
    peak = up[: len(x) * 4].reshape(len(x), 4).max(axis=1)
    need = np.minimum(1.0, 10 ** (ceiling_db / 20) / np.maximum(peak, 1e-12))
    ahead = int(0.0015 * SR)
    attack = minimum_filter1d(need, 2 * ahead + 1)
    # Hold each reduction for 10 ms, then recover along a one-pole curve; the
    # minimum with `attack` keeps every peak under the ceiling.
    hold = int(0.01 * SR)
    held = minimum_filter1d(attack, hold, origin=(hold - 1) // 2)
    c = 1 - math.exp(-1 / (0.06 * SR))
    released = signal.lfilter([c], [1, c - 1], held, zi=[1 - c])[0]
    gain = np.minimum(released, attack)
    kernel = np.hanning(ahead + 2)[1:-1]
    gain = np.convolve(gain, kernel / kernel.sum(), "same")
    return x * gain[:, None]


def master(mix: np.ndarray) -> tuple[np.ndarray, float]:
    gain_db = 0.0
    out = mix
    for _ in range(4):
        level = loudness(out)["integrated_lufs"]
        gain_db += TARGET_LUFS - level
        out = limit(mix * 10 ** (gain_db / 20), CEILING_DBTP - 0.3)
        if abs(loudness(out)["integrated_lufs"] - TARGET_LUFS) < 0.1:
            break
    # The film ends on this sample; let whatever remains close without a click.
    return fade(out, 0, 0.25), gain_db


# ---------------------------------------------------------------------------
# Checks


def measured_attack(env: np.ndarray, at: float, lead: int = 0, window: float = HIT_WINDOW) -> tuple[float, float, float]:
    """Read a cue back from a rendered stem: attack time (s), peak dBFS, and
    the loudest level in the 30 ms before the sound begins, relative to its peak.
    `lead` is how far the sound's first onset sits before its attack."""
    first = round(at * SR) - lead
    attack, peak = attack_in(env, first - int(LEAD_IN * SR), first + int(window * SR))
    pre = env[max(0, first - int(0.03 * SR)): max(0, first - int(0.004 * SR))]
    pre_db = 20 * math.log10((pre.max() if len(pre) else 0) / peak + 1e-9)
    return attack / SR, 20 * math.log10(peak + 1e-12), pre_db


def energy_db(x: np.ndarray, at: float, length: float = 0.1) -> float:
    a = round(at * SR)
    seg = x[a: a + int(length * SR)]
    return 10 * math.log10(np.mean(seg ** 2) + 1e-15)


def check(placed: list[Placed], sfx: np.ndarray, music: np.ndarray, final: np.ndarray, gain_db: float) -> dict:
    scale = 10 ** (gain_db / 20)
    highpass = signal.butter(2, 1000, "highpass", fs=SR, output="sos")
    hi_sfx, hi_music = signal.sosfilt(highpass, sfx, axis=0), signal.sosfilt(highpass, music, axis=0)
    env_sfx, env_music = level(sfx), level(music)
    rows = []
    for p in placed:
        if p.cue in ECHO and p.voice != CUE_VOICES[p.cue]:
            continue
        t, peak_db, pre_db = measured_attack(env_sfx, p.at, p.offset(p.gesture.attack - p.gesture.first), VOICES[p.voice].window)
        # Audibility: the transient's first 10 ms above 1 kHz, where clicks and
        # snips live and where the music would have to mask them.
        ratio = energy_db(hi_sfx, p.at, 0.01) - energy_db(hi_music, p.at, 0.01)
        rows.append({
            "cue": p.cue, "kind": p.kind, "beat": round(p.at / BEAT, 3), "time": round(p.at, 4), "frame": p.at * FPS,
            "voice": p.voice, "source": int(p.gesture.source), "slice_ms": round(p.gesture.attack / SR * 1000, 1),
            "shift_st": round(p.shift, 2), "error_ms": round((t - p.at) * 1000, 2), "peak_db": round(peak_db + gain_db, 1),
            "pre_db": round(pre_db, 1), "over_music_db": round(ratio, 1),
        })
    # The greeting and the last chord are notes, so they are timed in the music.
    for cid in sorted(PLAYED):
        for b in SCORE["cues"][cid]["at"]:
            # A chord is read from its first 12 ms: a low pizzicato keeps
            # swelling for tens of milliseconds after the pluck.
            t, peak_db, pre_db = measured_attack(env_music, sec(b), window=NOTE_WINDOW)
            rows.append({"cue": cid, "kind": SCORE["cues"][cid]["kind"], "beat": b, "time": round(sec(b), 4), "frame": sec(b) * FPS,
                         "voice": "instrument", "error_ms": round((t - sec(b)) * 1000, 2), "peak_db": round(peak_db + gain_db, 1),
                         "pre_db": round(pre_db, 1), "over_music_db": None})
    sfx_times = sorted(p.at for p in placed)
    for r in rows:
        # Only a sound that starts out of quiet, with no other cue sounding in
        # its first 80 ms, can be read back on its own.
        r["clear"] = r["pre_db"] <= -12
        if r["voice"] == "instrument":
            r["alone"] = True
        else:
            near = [t for t in sfx_times if -0.005 < t - r["time"] < HIT_WINDOW]
            r["alone"] = len(near) <= (2 if r["cue"] in ECHO else 1)
    rows.sort(key=lambda r: (r["time"], r["cue"]))
    stats = loudness(final)
    hush = sec(SCORE["cues"]["hush"]["at"][0])
    idea = sec(SCORE["cues"]["idea"]["at"][0])
    music_in_hush = energy_db(music * scale, hush + 0.02, idea - hush - 0.04)
    # The film draws the marble touching down at these moments; the recording has to agree.
    drawn = SCORE["cues"]["hush"]["bounces"]
    lead = 0.02
    seg = sfx[round((hush - lead) * SR): round((hush + drawn[-1] + 0.03) * SR)]
    db = envelope_db(seg)
    heard = [f * HOP / SR - lead for f in onset_frames(db, float(db.max()) - 40)]
    bounce_errors = [min((abs(h - b) for h in heard), default=math.inf) * 1000 for b in drawn]
    keys = [(r["source"], r["slice_ms"], r["shift_st"]) for r in rows if r["kind"] in ("cut", "click")]
    failures = []
    for r in rows:
        # A recording slice lands within 1 ms. A note is read back from the
        # polyphonic music stem, where 5 ms (under a third of a frame) is the
        # honest resolution.
        tolerance = 5.0 if r["voice"] == "instrument" else 1.0
        if r["clear"] and r["alone"] and abs(r["error_ms"]) > tolerance:
            failures.append(f"{r['cue']} @ beat {r['beat']}: attack off by {r['error_ms']} ms")
        # Cuts and clicks must each stand alone; other hits may overlap like a drum pattern.
        if r["kind"] in ("cut", "click") and not r["clear"]:
            failures.append(f"{r['cue']} @ beat {r['beat']}: masked by the sound before it ({r['pre_db']} dB)")
        if r["kind"] in ("cut", "click") and r["over_music_db"] < 6:
            failures.append(f"{r['cue']} @ beat {r['beat']}: only {r['over_music_db']} dB over the music")
        # Standing out of silence is not enough; a cut or click must be plainly loud.
        if r["kind"] in ("cut", "click") and r["peak_db"] < -24:
            failures.append(f"{r['cue']} @ beat {r['beat']}: peaks at only {r['peak_db']} dBFS")
        if abs(r["frame"] - round(r["frame"])) > 1e-6:
            failures.append(f"{r['cue']} @ beat {r['beat']}: not on a frame boundary")
    if len(set(keys)) != len(keys):
        failures.append("a cut or click reuses another's recording slice")
    if abs(stats["integrated_lufs"] - TARGET_LUFS) > 0.5:
        failures.append(f"integrated loudness {stats['integrated_lufs']:.2f} LUFS, target {TARGET_LUFS}")
    if stats["true_peak_dbtp"] > CEILING_DBTP:
        failures.append(f"true peak {stats['true_peak_dbtp']:.2f} dBTP over {CEILING_DBTP}")
    if music_in_hush > -70:
        failures.append(f"music rings into the hush at {music_in_hush:.1f} dBFS")
    if max(bounce_errors) > 5:
        failures.append(f"the hush marble's bounces are {max(bounce_errors):.1f} ms from where the film draws them")
    if len(final) != LENGTH:
        failures.append("length does not match the film")
    return {"loudness": stats, "gain_db": gain_db, "cues": rows, "music_in_hush_dbfs": music_in_hush,
            "hush_bounce_errors_ms": [round(e, 1) for e in bounce_errors],
            "unique_cut_click_slices": len(set(keys)), "cut_click_cues": len(keys), "failures": failures}


def summary(report: dict) -> str:
    rows = report["cues"]
    s = report["loudness"]
    solo = [r for r in rows if r["clear"] and r["alone"] and r["voice"] != "instrument"]
    notes = [r for r in rows if r["voice"] == "instrument"]
    err = [abs(r["error_ms"]) for r in solo]
    cc = [r for r in rows if r["kind"] in ("cut", "click")]
    lines = [
        f"{len(rows)} cues; {len(solo)} recorded sounds that can be read back alone land within {max(err):.2f} ms "
        f"(mean {np.mean(err):.2f} ms; 1 frame = {1000 / FPS:.1f} ms); the other {len(rows) - len(solo) - len(notes)} overlap "
        f"another sound; {len(notes)} instrument notes within {max(abs(r['error_ms']) for r in notes):.2f} ms",
        f"cuts and clicks: {sum(r['clear'] for r in cc)}/{len(cc)} start out of quiet, worst error "
        f"{max(abs(r['error_ms']) for r in cc):.2f} ms",
        f"{report['unique_cut_click_slices']}/{report['cut_click_cues']} cuts and clicks use distinct recording slices",
        f"lowest cut/click over the music: {min(r['over_music_db'] for r in rows if r['kind'] in ('cut', 'click')):.1f} dB",
        f"integrated {s['integrated_lufs']:.2f} LUFS, true peak {s['true_peak_dbtp']:.2f} dBTP, LRA {s['lra_lu']:.1f} LU, "
        f"max short-term {s['max_short_term_lufs']:.1f} LUFS, max momentary {s['max_momentary_lufs']:.1f} LUFS",
        f"hush: music {report['music_in_hush_dbfs']:.1f} dBFS; "
        f"marble bounces within {max(report['hush_bounce_errors_ms']):.1f} ms of the drawn ones",
    ]
    lines += [f"FAIL {f}" for f in report["failures"]] or ["all checks passed"]
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# The exported video


def ffmpeg(*args: str) -> bytes:
    exe = shutil.which("ffmpeg")
    if not exe:
        raise SystemExit("--verify needs ffmpeg on PATH")
    return subprocess.run([exe, "-v", "error", *args], check=True, capture_output=True).stdout


def verify(video: Path) -> list[str]:
    ref, _ = sf.read(OUT / "score.wav", dtype="float64", always_2d=True)
    got = np.frombuffer(ffmpeg("-i", str(video), "-f", "f32le", "-ac", "2", "-ar", str(SR), "-"), dtype="<f4").reshape(-1, 2).astype(np.float64)
    a, b = ref[: 20 * SR].mean(axis=1), got[: 20 * SR].mean(axis=1)
    corr = signal.correlate(b, a, mode="full", method="fft")
    lag = int(np.argmax(corr)) - (len(a) - 1)
    frames = np.frombuffer(ffmpeg("-i", str(video), "-vf", "scale=160:90,format=gray", "-f", "rawvideo", "-"), dtype=np.uint8)
    frames = frames.reshape(-1, 90, 160).astype(np.float32)
    diff = np.abs(np.diff(frames, axis=0)).mean(axis=(1, 2))
    # A hard cut is a frame change far above the motion around it.
    local = np.array([np.median(diff[max(0, i - 8): i + 8]) for i in range(len(diff))])
    cuts = [i + 1 for i in range(len(diff)) if diff[i] > max(10.0, 5 * local[i])]
    notes = [f"video: {len(frames)} frames ({len(frames) / FPS:.3f} s), audio {len(got) / SR:.3f} s, audio offset {lag / SR * 1000:+.2f} ms"]
    missing = []
    for cid, c in SCORE["cues"].items():
        if c["kind"] != "cut":
            continue
        for b in c["at"]:
            f = round(sec(b) * FPS)
            # The cut is the biggest change within a frame of the cue.
            near = sorted((x for x in cuts if abs(x - f) <= 1), key=lambda x: -diff[x - 1])
            (notes if near and near[0] == f else missing).append(f"cut {cid} @ frame {f}: {'found at ' + str(near[0]) if near else 'NOT FOUND'}")
    notes += missing
    extra = [x for x in cuts if all(abs(x - round(sec(b) * FPS)) > 1 for c in SCORE["cues"].values() if c["kind"] == "cut" for b in c["at"])]
    notes.append(f"other hard changes (no cut cue): {extra[:20]}{' …' if len(extra) > 20 else ''}")
    if abs(lag) > SR // 1000 or missing:
        notes.append("FAIL")
    return notes


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--verify", type=Path, help="exported video to check against the score")
    args = parser.parse_args()
    if args.verify:
        notes = verify(args.verify)
        print("\n".join(notes))
        sys.exit(1 if notes[-1] == "FAIL" else 0)
    OUT.mkdir(parents=True, exist_ok=True)
    placed = plan()
    sfx = render_sfx(placed)
    swell = render_swell()
    music = duck(render_music(), sorted({p.at for p in placed if p.kind in ("cut", "click")}))
    final, gain_db = master(sfx + swell + music)
    report = check(placed, sfx, music + swell, final, gain_db)
    sf.write(OUT / "score.wav", final, SR, subtype="PCM_24")
    scale = 10 ** (gain_db / 20)
    sf.write(OUT / "score-sfx.wav", sfx * scale, SR, subtype="FLOAT")
    sf.write(OUT / "score-music.wav", (music + swell) * scale, SR, subtype="FLOAT")
    (OUT / "score-report.json").write_text(json.dumps(report, indent=1))
    print(summary(report))
    print(f"wrote {OUT / 'score.wav'}")
    sys.exit(1 if report["failures"] else 0)


if __name__ == "__main__":
    main()
