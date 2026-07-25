"""
Offline threshold calibration for the AI-detection features.

Runs all 10 stylometric features across the entire blog corpus (pulled from
Elasticsearch) and reports the empirical distribution of each raw feature value.
Because we have no ground-truth AI/human labels, we calibrate thresholds
*relative to the corpus*: the bulk of real blog content defines the human
baseline, and statistical outliers define the AI-like end. This replaces
hand-guessed thresholds with data-grounded ones.

Proposed thresholds (feed into FEATURE_THRESHOLDS in constants.py):
    - normal features (high raw = AI):   low = p50,  high = p90
    - inverted features (high raw = human): low = p10, high = p50

Run locally (not in the request path):
    python -m internal.models.calibrate_thresholds

Env (with local-dev defaults):
    OPENSEARCH_OS_HOST      default http://localhost:9201
    OPENSEARCH_OS_USERNAME  default admin
    OPENSEARCH_OS_PASSWORD  (required, no default — export before running)
    ES_BLOG_INDEX           default the_monkeys_blogs
"""

import os
import json
import base64
import urllib.request

from internal.services.ai_detection import (
    AIDetector,
    extract_paragraphs,
    _word_tokens,
    _split_sentences,
)
from internal.constants import (
    FEATURE_WEIGHTS,
    INVERTED_FEATURES,
    MIN_WORDS_FOR_ANALYSIS,
)

OUT_PATH = os.path.join(os.path.dirname(__file__), "calibration.json")
PCTS = [5, 10, 25, 50, 75, 90, 95]


def _fetch_sources(host, index, user, pwd):
    auth = base64.b64encode(f"{user}:{pwd}".encode()).decode()
    headers = {"Authorization": f"Basic {auth}", "Content-Type": "application/json"}
    body = json.dumps({"size": 500, "_source": ["blog", "blog_id"], "query": {"match_all": {}}})
    url = f"{host}/{index}/_search?scroll=2m"
    req = urllib.request.Request(url, data=body.encode(), headers=headers, method="POST")
    with urllib.request.urlopen(req) as resp:
        page = json.loads(resp.read())
    scroll_id = page.get("_scroll_id")
    while True:
        hits = page.get("hits", {}).get("hits", [])
        if not hits:
            break
        for h in hits:
            yield h.get("_source", {})
        body = json.dumps({"scroll": "2m", "scroll_id": scroll_id})
        req = urllib.request.Request(
            f"{host}/_search/scroll", data=body.encode(), headers=headers, method="POST"
        )
        with urllib.request.urlopen(req) as resp:
            page = json.loads(resp.read())
        scroll_id = page.get("_scroll_id")


def _raw_features(det, paragraphs):
    """Replicate score()'s preprocessing but return only raw feature values."""
    full_text = "\n\n".join(paragraphs)
    word_count = len(_word_tokens(full_text))
    lower_text = full_text.lower()
    sentences = _split_sentences(full_text)
    return {
        "ai_phrases": det._f_ai_phrases(lower_text, word_count)[0],
        "contraction_absence": det._f_contraction_absence(lower_text)[0],
        "burstiness": det._f_burstiness(sentences)[0],
        "passive_voice": det._f_passive_voice(full_text, len(sentences))[0],
        "punctuation_density": det._f_punctuation_density(full_text, word_count)[0],
        "paragraph_uniformity": det._f_paragraph_uniformity(paragraphs)[0],
        "repetitive_starters": det._f_repetitive_starters(sentences)[0],
        "personal_voice": det._f_personal_voice(full_text, lower_text, word_count)[0],
        "emoji_diversity": det._f_emoji_diversity(full_text)[0],
        "perplexity": det._f_perplexity(_word_tokens(lower_text))[0],
    }, word_count


def _percentile(sorted_vals, pct):
    if not sorted_vals:
        return 0.0
    k = (len(sorted_vals) - 1) * (pct / 100.0)
    lo = int(k)
    hi = min(lo + 1, len(sorted_vals) - 1)
    frac = k - lo
    return sorted_vals[lo] + (sorted_vals[hi] - sorted_vals[lo]) * frac


def main():
    host = os.getenv("OPENSEARCH_OS_HOST", "http://localhost:9201").rstrip("/")
    index = os.getenv("ES_BLOG_INDEX", "the_monkeys_blogs")
    user = os.getenv("OPENSEARCH_OS_USERNAME", "admin")
    pwd = os.getenv("OPENSEARCH_OS_PASSWORD", "")

    det = AIDetector()
    samples = {f: [] for f in FEATURE_WEIGHTS}
    analysed = 0

    for source in _fetch_sources(host, index, user, pwd):
        paragraphs = extract_paragraphs(source)
        if not paragraphs:
            continue
        raw, wc = _raw_features(det, paragraphs)
        if wc < MIN_WORDS_FOR_ANALYSIS:
            continue
        analysed += 1
        for f, v in raw.items():
            samples[f].append(v)

    report = {}
    proposed = {}
    for f in FEATURE_WEIGHTS:
        vals = sorted(samples[f])
        pcts = {f"p{p}": round(_percentile(vals, p), 4) for p in PCTS}
        report[f] = pcts
        if f in INVERTED_FEATURES:
            low, high = pcts["p10"], pcts["p50"]
        else:
            low, high = pcts["p50"], pcts["p90"]
        # guard against degenerate low==high
        if high <= low:
            high = low + 1e-6
        proposed[f] = (round(low, 4), round(high, 4))

    with open(OUT_PATH, "w", encoding="utf-8") as fh:
        json.dump(
            {"doc_count": analysed, "percentiles": report, "proposed_thresholds": proposed},
            fh,
            ensure_ascii=False,
            indent=2,
        )

    print(f"\nCalibrated on {analysed} blogs (>= {MIN_WORDS_FOR_ANALYSIS} words)\n")
    header = "feature".ljust(22) + "".join(f"p{p}".rjust(9) for p in PCTS) + "   inverted"
    print(header)
    print("-" * len(header))
    for f in FEATURE_WEIGHTS:
        row = f.ljust(22)
        row += "".join(f"{report[f]['p'+str(p)]:>9.3f}" for p in PCTS)
        row += "   yes" if f in INVERTED_FEATURES else "    no"
        print(row)

    print("\nProposed FEATURE_THRESHOLDS (low, high):")
    for f, (lo, hi) in proposed.items():
        print(f'    "{f}": ({lo}, {hi}),')
    print(f"\nWritten -> {OUT_PATH}")


if __name__ == "__main__":
    main()
