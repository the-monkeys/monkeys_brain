"""
Offline builder for the unigram word-frequency model used by the perplexity
feature. Pulls blog text from Elasticsearch, counts words, and writes
word_freq.json next to this script.

Run locally (not in the request path):
    python -m internal.models.build_word_freq

Env (with local-dev defaults):
    OPENSEARCH_OS_HOST      default http://localhost:9201
    OPENSEARCH_OS_USERNAME  default admin
    OPENSEARCH_OS_PASSWORD  (required, no default — export before running)
    ES_BLOG_INDEX           default the_monkeys_blogs
"""

import os
import re
import json
import base64
import urllib.request
from collections import Counter

TOP_N = 20000       # keep this many most-frequent words
MIN_COUNT = 2       # drop words seen fewer than this many times
WORD_RE = re.compile(r"\b[a-z][a-z'-]*\b")

_TAG_RE = re.compile(r"<[^>]+>")
_ENTITY_RE = re.compile(r"&[a-zA-Z]+;|&#\d+;")
_URL_RE = re.compile(r"https?://\S+|www\.\S+")
# HTML/URL leftovers that survive tokenisation but aren't real words
_STOP_NOISE = {"nbsp", "amp", "quot", "href", "https", "http", "www", "rel", "noopener"}

OUT_PATH = os.path.join(os.path.dirname(__file__), "word_freq.json")


def _clean(text):
    text = _URL_RE.sub(" ", text)
    text = _TAG_RE.sub(" ", text)
    text = _ENTITY_RE.sub(" ", text)
    return text


def _extract_text(source):
    blocks = source.get("blog", {}).get("blocks", [])
    parts = []
    for b in blocks:
        if not isinstance(b, dict):
            continue
        data = b.get("data", {})
        if not isinstance(data, dict):
            continue
        t = b.get("type", "")
        if t in ("header", "paragraph", "quote"):
            parts.append(str(data.get("text", "")))
        elif t == "list":
            for it in data.get("items", []):
                if isinstance(it, str):
                    parts.append(it)
                elif isinstance(it, dict):
                    parts.append(str(it.get("content", "")))
    return _clean(" ".join(parts))


def _fetch_sources(host, index, user, pwd):
    """Scroll the whole index and yield each _source."""
    auth = base64.b64encode(f"{user}:{pwd}".encode()).decode()
    headers = {"Authorization": f"Basic {auth}", "Content-Type": "application/json"}

    body = json.dumps({"size": 500, "_source": ["blog"], "query": {"match_all": {}}})
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


def main():
    host = os.getenv("OPENSEARCH_OS_HOST", "http://localhost:9201").rstrip("/")
    index = os.getenv("ES_BLOG_INDEX", "the_monkeys_blogs")
    user = os.getenv("OPENSEARCH_OS_USERNAME", "admin")
    pwd = os.getenv("OPENSEARCH_OS_PASSWORD", "")

    counts = Counter()
    doc_count = 0
    for source in _fetch_sources(host, index, user, pwd):
        text = _extract_text(source).lower()
        if not text.strip():
            continue
        doc_count += 1
        counts.update(WORD_RE.findall(text))

    pruned = {w: c for w, c in counts.items() if c >= MIN_COUNT and w not in _STOP_NOISE}
    top = dict(sorted(pruned.items(), key=lambda kv: kv[1], reverse=True)[:TOP_N])
    total_tokens = sum(top.values())

    model = {
        "doc_count": doc_count,
        "total_tokens": total_tokens,
        "vocab_size": len(top),
        "vocab": top,
    }
    with open(OUT_PATH, "w", encoding="utf-8") as f:
        json.dump(model, f, ensure_ascii=False)

    print(
        f"Built word_freq.json from {doc_count} docs: "
        f"{len(top)} words, {total_tokens} tokens -> {OUT_PATH}"
    )


if __name__ == "__main__":
    main()
