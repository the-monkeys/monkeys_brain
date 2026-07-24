"""Rule-based AI content detector. Scores blog text 0 (human) to 10 (AI).
See DESIGN.md for feature definitions."""

import re
import os
import json
import math
import logging
from collections import Counter
from typing import Dict, Any, List, Tuple

from ..constants import (
    AI_ANALYZER_VERSION,
    AI_FLAG_THRESHOLD,
    MIN_WORDS_FOR_ANALYSIS,
    AI_HARD_EM_DASH,
    AI_STRONG_PHRASES,
    PERPLEXITY_PPL_BOUNDS,
    PERPLEXITY_OOV_BOUNDS,
    VERDICT_BANDS,
    FEATURE_WEIGHTS,
    FEATURE_THRESHOLDS,
    INVERTED_FEATURES,
    AI_PHRASES,
    CONTRACTION_PAIRS,
    PERSONAL_MARKERS,
    HUMAN_SLANG,
    PERSONAL_PRONOUNS,
    COMMON_EMOJIS,
)

logger = logging.getLogger(__name__)

# unigram frequency model for the perplexity feature (loaded once, lazily)
_WORD_FREQ: Dict[str, Any] = None
_WORD_FREQ_LOADED = False
_WORD_FREQ_PATH = os.path.join(
    os.path.dirname(os.path.dirname(__file__)), "models", "word_freq.json"
)


def _load_word_freq():
    global _WORD_FREQ, _WORD_FREQ_LOADED
    if _WORD_FREQ_LOADED:
        return _WORD_FREQ
    _WORD_FREQ_LOADED = True
    try:
        with open(_WORD_FREQ_PATH, encoding="utf-8") as f:
            m = json.load(f)
        total = m.get("total_tokens", 0)
        vocab = m.get("vocab", {})
        if total > 0 and vocab:
            _WORD_FREQ = {"total": total, "vocab": vocab}
            logger.info(
                f"perplexity model loaded: {len(vocab)} words, {total} tokens"
            )
    except FileNotFoundError:
        logger.warning("perplexity model missing; feature runs neutral")
    except Exception as e:
        logger.warning(f"perplexity model failed to load: {e}")
    return _WORD_FREQ

_EMOJI_PATTERN = re.compile(
    "["
    "\U0001F300-\U0001F5FF"
    "\U0001F600-\U0001F64F"
    "\U0001F680-\U0001F6FF"
    "\U0001F1E0-\U0001F1FF"
    "\U00002700-\U000027BF"
    "\U0001F900-\U0001F9FF"
    "\U00002600-\U000026FF"
    "]+",
    flags=re.UNICODE,
)

# form of "to be" + past participle
_PASSIVE_PATTERN = re.compile(
    r"\b(is|are|was|were|be|been|being)\s+(\w+ed|\w+en)\b",
    flags=re.IGNORECASE,
)

_HTML_TAG_PATTERN = re.compile(r"<[^>]+>")
_HTML_ENTITY_PATTERN = re.compile(r"&[a-zA-Z]+;|&#\d+;")


def _clamp(value: float, low: float = 0.0, high: float = 1.0) -> float:
    return max(low, min(high, value))


def _normalise(feature: str, raw: float) -> float:
    low, high = FEATURE_THRESHOLDS[feature]
    if high == low:
        return 0.0
    if feature in INVERTED_FEATURES:
        norm = (high - raw) / (high - low)
    else:
        norm = (raw - low) / (high - low)
    return _clamp(norm)


def _clean_text(text: str) -> str:
    text = _HTML_TAG_PATTERN.sub(" ", text)
    text = _HTML_ENTITY_PATTERN.sub(" ", text)
    return text


def _split_sentences(text: str) -> List[str]:
    parts = re.split(r"[.!?]+", text)
    return [s.strip() for s in parts if s.strip()]


def _word_tokens(text: str) -> List[str]:
    return re.findall(r"\b\w+\b", text)


class AIDetector:
    """Scores text 0 (human) to 10 (AI)."""

    def score(self, paragraphs: List[str]) -> Dict[str, Any]:
        full_text = "\n\n".join(paragraphs)
        words = _word_tokens(full_text)
        word_count = len(words)

        if word_count < MIN_WORDS_FOR_ANALYSIS:
            return self._empty_result(
                word_count,
                len(full_text),
                reason=f"Text too short for reliable analysis "
                f"({word_count} < {MIN_WORDS_FOR_ANALYSIS} words)",
            )

        lower_text = full_text.lower()
        sentences = _split_sentences(full_text)

        raw: Dict[str, float] = {}
        evidence: Dict[str, str] = {}

        raw["ai_phrases"], evidence["ai_phrases"] = self._f_ai_phrases(
            lower_text, word_count
        )
        raw["contraction_absence"], evidence["contraction_absence"] = (
            self._f_contraction_absence(lower_text)
        )
        raw["burstiness"], evidence["burstiness"] = self._f_burstiness(sentences)
        raw["passive_voice"], evidence["passive_voice"] = self._f_passive_voice(
            full_text, len(sentences)
        )
        raw["punctuation_density"], evidence["punctuation_density"] = (
            self._f_punctuation_density(full_text, word_count)
        )
        raw["paragraph_uniformity"], evidence["paragraph_uniformity"] = (
            self._f_paragraph_uniformity(paragraphs)
        )
        raw["repetitive_starters"], evidence["repetitive_starters"] = (
            self._f_repetitive_starters(sentences)
        )
        raw["personal_voice"], evidence["personal_voice"] = self._f_personal_voice(
            full_text, lower_text, word_count
        )
        raw["emoji_diversity"], evidence["emoji_diversity"] = self._f_emoji_diversity(
            full_text
        )
        raw["perplexity"], evidence["perplexity"] = self._f_perplexity(
            _word_tokens(lower_text)
        )

        normalised: Dict[str, float] = {}
        weighted_sum = 0.0
        for feature, weight in FEATURE_WEIGHTS.items():
            n = _normalise(feature, raw[feature])
            normalised[feature] = round(n, 4)
            weighted_sum += weight * n

        final_score = round(weighted_sum * 10, 1)

        # informational tells only — they do NOT override the weighted score.
        # em-dashes already feed punctuation_density; strong phrases feed
        # ai_phrases. this list is surfaced purely as human-readable evidence.
        tells, tell_count = self._detect_tells(full_text, lower_text)

        verdict = self._verdict(final_score)
        reason = "Rule-based stylometric analysis of blog text"

        return {
            "score": final_score,
            "verdict": verdict,
            "is_flagged": final_score >= AI_FLAG_THRESHOLD,
            "analyzer_version": AI_ANALYZER_VERSION,
            "reason": reason,
            "features": normalised,
            "feature_evidence": evidence,
            "tells": tells,
            "tell_count": tell_count,
            "stats": {
                "text_length": len(full_text),
                "word_count": word_count,
                "sentence_count": len(sentences),
                "paragraph_count": len([p for p in paragraphs if p.strip()]),
            },
        }

    # each feature returns (raw_value, evidence_string)

    def _f_ai_phrases(self, lower_text: str, word_count: int) -> Tuple[float, str]:
        found = []
        total = 0
        for phrase in AI_PHRASES:
            c = lower_text.count(phrase)
            if c > 0:
                total += c
                found.append(f"{phrase} x{c}")
        density = total / (word_count + 1)
        ev = ", ".join(found[:5]) if found else "none"
        return density, f"{total} matches ({ev})"

    def _f_contraction_absence(self, lower_text: str) -> Tuple[float, str]:
        expanded = 0
        contracted = 0
        for exp, con in CONTRACTION_PAIRS:
            expanded += len(re.findall(r"\b" + re.escape(exp) + r"\b", lower_text))
            contracted += lower_text.count(con)
        total = expanded + contracted
        if total == 0:
            return 0.5, "no contraction data (neutral)"
        ratio = expanded / total
        return ratio, f"{expanded} expanded, {contracted} contracted"

    def _f_burstiness(self, sentences: List[str]) -> Tuple[float, str]:
        lengths = [len(_word_tokens(s)) for s in sentences]
        if len(lengths) < 2:
            return 0.6, "too few sentences (assumed human)"
        mean = sum(lengths) / len(lengths)
        if mean == 0:
            return 0.6, "empty sentences"
        variance = sum((x - mean) ** 2 for x in lengths) / len(lengths)
        cv = math.sqrt(variance) / mean
        return cv, f"cv={cv:.2f}"

    def _f_passive_voice(self, text: str, sentence_count: int) -> Tuple[float, str]:
        if sentence_count == 0:
            return 0.0, "no sentences"
        matches = len(_PASSIVE_PATTERN.findall(text))
        ratio = matches / sentence_count
        return ratio, f"{matches} passive constructions ({ratio*100:.0f}% of sentences)"

    def _f_punctuation_density(self, text: str, word_count: int) -> Tuple[float, str]:
        # drop colons inside URLs / times
        cleaned = re.sub(r"https?://\S+", " ", text)
        cleaned = re.sub(r"\d:\d", " ", cleaned)
        em = cleaned.count("—") + cleaned.count("–")
        colon = cleaned.count(":")
        semi = cleaned.count(";")
        total = em + colon + semi
        density = total / (word_count + 1)
        return density, f"{em} em/en-dash, {colon} colon, {semi} semicolon"

    def _f_paragraph_uniformity(self, paragraphs: List[str]) -> Tuple[float, str]:
        lengths = [len(_word_tokens(p)) for p in paragraphs]
        lengths = [n for n in lengths if n > 5]
        if len(lengths) < 2:
            return 0.3, "too few paragraphs (neutral)"
        mean = sum(lengths) / len(lengths)
        if mean == 0:
            return 0.3, "empty paragraphs"
        variance = sum((x - mean) ** 2 for x in lengths) / len(lengths)
        cv = math.sqrt(variance) / mean
        return cv, f"cv={cv:.2f} across {len(lengths)} paragraphs"

    def _f_repetitive_starters(self, sentences: List[str]) -> Tuple[float, str]:
        if len(sentences) < 3:
            return 0.15, "too few sentences (assumed human)"
        starters = []
        for s in sentences:
            toks = _word_tokens(s)
            if toks:
                starters.append(toks[0].lower())
        if not starters:
            return 0.15, "no starters"
        counts = Counter(starters)
        top_word, top_count = counts.most_common(1)[0]
        ratio = top_count / len(starters)
        return ratio, f"'{top_word}' starts {ratio*100:.0f}% of sentences"

    def _f_personal_voice(
        self, text: str, lower_text: str, word_count: int
    ) -> Tuple[float, str]:
        marker_count = sum(lower_text.count(m) for m in PERSONAL_MARKERS)
        slang_count = 0
        for term in HUMAN_SLANG:
            slang_count += len(re.findall(r"\b" + re.escape(term) + r"\b", lower_text))
        pronoun_count = 0
        for p in PERSONAL_PRONOUNS:
            pronoun_count += len(re.findall(r"\b" + re.escape(p) + r"\b", lower_text))
        pronoun_count += len(re.findall(r"\bI\b", text))  # case-sensitive
        # slang is a strong human tell, so it counts triple
        density = (pronoun_count + marker_count * 2 + slang_count * 3) / (word_count + 1)
        return density, f"{pronoun_count} pronouns, {marker_count} markers, {slang_count} slang"

    def _f_emoji_diversity(self, text: str) -> Tuple[float, str]:
        runs = _EMOJI_PATTERN.findall(text)
        flat = [ch for run in runs for ch in run]  # split grouped runs
        if not flat:
            return 0.0, "no emojis"
        total = len(flat)
        unique = len(set(flat))
        uncommon = sum(1 for e in flat if e not in COMMON_EMOJIS)
        diversity = unique / total
        uncommon_ratio = uncommon / total
        score = 0.6 * diversity + 0.4 * uncommon_ratio
        return score, f"{total} emojis, {unique} unique, {uncommon} uncommon"

    def _f_perplexity(self, lower_words: List[str]) -> Tuple[float, str]:
        """Predictability against the human-blog unigram model. Low perplexity
        (predictable word choice) and low out-of-vocab rate both lean AI."""
        model = _load_word_freq()
        if not model:
            return 0.5, "model unavailable (neutral)"
        total = model["total"]
        vocab = model["vocab"]
        surprisals = []
        oov = 0
        for w in lower_words:
            c = vocab.get(w)
            if c:
                surprisals.append(-math.log2(c / total))
            else:
                oov += 1
        if len(surprisals) < 20:
            return 0.5, "too few in-vocab words (neutral)"
        mean_s = sum(surprisals) / len(surprisals)
        ppl = 2 ** mean_s
        oov_rate = oov / len(lower_words)

        p_low, p_high = PERPLEXITY_PPL_BOUNDS
        o_low, o_high = PERPLEXITY_OOV_BOUNDS
        ppl_ai = _clamp((p_high - ppl) / (p_high - p_low))
        oov_ai = _clamp((o_high - oov_rate) / (o_high - o_low))
        ai_ness = 0.6 * ppl_ai + 0.4 * oov_ai
        return ai_ness, f"ppl={ppl:.0f}, oov={oov_rate*100:.0f}%"

    def _detect_tells(self, text: str, lower_text: str) -> Tuple[List[str], int]:
        """Human-readable AI tells for transparency. These are surfaced as
        evidence ONLY and never override the weighted score."""
        tells = []
        count = 0

        em = text.count(AI_HARD_EM_DASH)
        if em:
            tells.append(f"em_dash x{em}")
            count += em

        runs = _EMOJI_PATTERN.findall(text)
        flat = [ch for run in runs for ch in run]
        uncommon = sum(1 for e in flat if e not in COMMON_EMOJIS)
        if uncommon:
            tells.append(f"unusual_emoji x{uncommon}")
            count += uncommon

        hits = []
        for p in AI_STRONG_PHRASES:
            c = lower_text.count(p)
            if c:
                hits.append(f"{p} x{c}")
                count += c
        if hits:
            tells.append("phrase: " + ", ".join(hits))

        return tells, count

    @staticmethod
    def _verdict(score: float) -> str:
        for upper, label in VERDICT_BANDS:
            if score <= upper:
                return label
        return VERDICT_BANDS[-1][1]

    @staticmethod
    def _empty_result(word_count: int, text_length: int, reason: str) -> Dict[str, Any]:
        return {
            "score": 0.0,
            "verdict": "human",
            "is_flagged": False,
            "analyzer_version": AI_ANALYZER_VERSION,
            "reason": reason,
            "features": {},
            "feature_evidence": {},
            "tells": [],
            "tell_count": 0,
            "stats": {
                "text_length": text_length,
                "word_count": word_count,
                "sentence_count": 0,
                "paragraph_count": 0,
            },
        }


# stateless, safe to reuse
_detector = AIDetector()


def extract_paragraphs(blog_data: Dict[str, Any]) -> List[str]:
    """Pull text from _source.blog.blocks[] (header/paragraph/quote/list)."""
    blocks = blog_data.get("blog", {}).get("blocks", [])
    paragraphs: List[str] = []

    for block in blocks:
        if not isinstance(block, dict):
            continue
        btype = block.get("type", "")
        data = block.get("data", {})
        if not isinstance(data, dict):
            continue

        if btype in ("header", "paragraph", "quote"):
            text = data.get("text", "")
            if text:
                cleaned = _clean_text(text).strip()
                if cleaned:
                    paragraphs.append(cleaned)
        elif btype == "list":
            for item in data.get("items", []):
                if isinstance(item, str) and item.strip():
                    paragraphs.append(_clean_text(item).strip())
                elif isinstance(item, dict) and item.get("content"):
                    paragraphs.append(_clean_text(str(item["content"])).strip())

    return paragraphs


def analyze_blog_json(blog_data: Dict[str, Any]) -> Dict[str, Any]:
    """Entry point: analyze a blog document, return the scoring dict."""
    blog_id = blog_data.get("blog_id") or blog_data.get("id", "unknown")
    try:
        paragraphs = extract_paragraphs(blog_data)
        if not paragraphs:
            result = AIDetector._empty_result(0, 0, "No text content found")
        else:
            result = _detector.score(paragraphs)
    except Exception as e:  # never crash the consumer
        logger.error(f"Error analyzing blog {blog_id}: {e}", exc_info=True)
        result = AIDetector._empty_result(0, 0, f"Analysis error: {e}")

    result["blog_id"] = blog_id
    return result
