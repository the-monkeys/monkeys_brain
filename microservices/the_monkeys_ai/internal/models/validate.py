"""
Offline validation harness for the AI-detection engine.

Runs the full feature pipeline over a labeled sample set and reports:
  - per-sample final score + verdict
  - per-feature separation between human and AI (mean gap + rank AUC)
  - surprisal-burstiness direction test (settles the DeepSeek proposal on
    real labeled data instead of theory)
  - classification metrics across a threshold sweep (accuracy / precision /
    recall / false-positive rate)

This is a LOCAL evaluation tool, never in the request path.

Layout:
    validation_data/human/*.txt   -> known human blogs (one per file)
    validation_data/ai/*.txt      -> known AI blogs (one per file)
    (paragraphs separated by newlines within each file)

Run:
    python -m internal.models.validate
"""

import os
import glob
import math

from internal.services.ai_detection import (
    AIDetector,
    _load_word_freq,
    _clean_text,
    _word_tokens,
)
from internal.constants import FEATURE_WEIGHTS, INVERTED_FEATURES, AI_FLAG_THRESHOLD

BASE = os.path.join(os.path.dirname(__file__), "..", "..", "validation_data")


def _load_samples(label):
    out = []
    for path in sorted(glob.glob(os.path.join(BASE, label, "*.txt"))):
        with open(path, encoding="utf-8") as f:
            raw = f.read()
        paragraphs = [_clean_text(ln).strip() for ln in raw.splitlines() if ln.strip()]
        if paragraphs:
            out.append((os.path.basename(path), paragraphs))
    return out


def _surprisal_variance(paragraphs):
    """Raw variance of per-word surprisal against the human unigram model.
    DeepSeek's claim: high variance -> human, low -> AI."""
    model = _load_word_freq()
    if not model:
        return None
    total = model["total"]
    vocab = model["vocab"]
    words = _word_tokens("\n".join(paragraphs).lower())
    surprisals = [-math.log2(vocab[w] / total) for w in words if w in vocab]
    if len(surprisals) < 20:
        return None
    mean_s = sum(surprisals) / len(surprisals)
    return sum((s - mean_s) ** 2 for s in surprisals) / len(surprisals)


def _auc(human_vals, ai_vals):
    """Probability a random AI sample scores higher than a random human one.
    0.5 = no separation, 1.0 = perfect (higher = AI)."""
    if not human_vals or not ai_vals:
        return 0.5
    wins = 0.0
    for a in ai_vals:
        for h in human_vals:
            if a > h:
                wins += 1
            elif a == h:
                wins += 0.5
    return wins / (len(human_vals) * len(ai_vals))


def main():
    det = AIDetector()
    human = _load_samples("human")
    ai = _load_samples("ai")

    if not human or not ai:
        print("Need at least one sample in both validation_data/human and /ai")
        return

    rows = []  # (label_int, name, score, features, surprisal_var)
    for label_int, samples in ((0, human), (1, ai)):
        for name, paragraphs in samples:
            res = det.score(paragraphs)
            rows.append(
                (label_int, name, res["score"], res["features"],
                 _surprisal_variance(paragraphs))
            )

    print(f"\nSamples: {len(human)} human, {len(ai)} AI\n")
    print("label   name                     score  verdict-side")
    print("-" * 56)
    for lab, name, score, _, _ in rows:
        side = "AI " if score >= AI_FLAG_THRESHOLD else "human"
        print(f"{'AI ' if lab else 'HUM':<7} {name:<24} {score:>5.1f}  {side}")

    # per-feature separation
    print("\nPer-feature separation (mean human vs AI, AUC; AUC>0.5 => higher=AI):")
    print(f"{'feature':<22}{'weight':>7}{'human':>9}{'ai':>9}{'auc':>8}  inv")
    print("-" * 64)
    for f in FEATURE_WEIGHTS:
        hv = [r[3].get(f, 0.0) for r in rows if r[0] == 0]
        av = [r[3].get(f, 0.0) for r in rows if r[0] == 1]
        hm = sum(hv) / len(hv)
        am = sum(av) / len(av)
        auc = _auc(hv, av)
        inv = "yes" if f in INVERTED_FEATURES else ""
        print(f"{f:<22}{FEATURE_WEIGHTS[f]:>7.2f}{hm:>9.3f}{am:>9.3f}{auc:>8.2f}  {inv}")

    # surprisal-burstiness test (DeepSeek)
    hvar = [r[4] for r in rows if r[0] == 0 and r[4] is not None]
    avar = [r[4] for r in rows if r[0] == 1 and r[4] is not None]
    if hvar and avar:
        hm = sum(hvar) / len(hvar)
        am = sum(avar) / len(avar)
        # DeepSeek predicts human variance > AI variance
        auc = _auc(hvar, avar)  # >0.5 means AI has HIGHER variance (theory broken)
        print("\nSurprisal-burstiness test (DeepSeek: expect human > AI):")
        print(f"  mean variance  human={hm:.3f}  ai={am:.3f}")
        verdict = "HOLDS" if hm > am else "REVERSED"
        print(f"  direction: {verdict}  (auc higher=AI = {auc:.2f})")

    # threshold sweep
    scores = [(r[0], r[2]) for r in rows]
    n_ai = sum(1 for l, _ in scores if l == 1)
    n_hum = sum(1 for l, _ in scores if l == 0)
    print("\nThreshold sweep:")
    print(f"{'thr':>5}{'acc':>8}{'prec':>8}{'recall':>8}{'fpr':>8}")
    print("-" * 37)
    for thr in [3.0, 3.5, 4.0, 4.5, 5.0, 5.5, 6.0, 6.5, 7.0]:
        tp = sum(1 for l, s in scores if l == 1 and s >= thr)
        fp = sum(1 for l, s in scores if l == 0 and s >= thr)
        tn = n_hum - fp
        acc = (tp + tn) / len(scores)
        prec = tp / (tp + fp) if (tp + fp) else 0.0
        rec = tp / n_ai if n_ai else 0.0
        fpr = fp / n_hum if n_hum else 0.0
        print(f"{thr:>5.1f}{acc:>8.2f}{prec:>8.2f}{rec:>8.2f}{fpr:>8.2f}")

    print("\nNote: metrics are only as trustworthy as the sample size. "
          "Add more labeled .txt files to both folders for a real number.")


if __name__ == "__main__":
    main()
