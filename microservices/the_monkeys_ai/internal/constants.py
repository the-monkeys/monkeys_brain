# Elasticsearch
ES_BLOG_INDEX = "the_monkeys_blogs"

# AI detection
AI_ANALYZER_VERSION = "1.2.0"
AI_FLAG_THRESHOLD = 6.0
MIN_WORDS_FOR_ANALYSIS = 100

# em-dash character, surfaced as an informational tell (not a score override)
AI_HARD_EM_DASH = "—"  # U+2014

# perplexity feature bounds (calibrated against the blog corpus model):
# lower perplexity and lower out-of-vocab rate both lean AI
PERPLEXITY_PPL_BOUNDS = (900.0, 2000.0)
PERPLEXITY_OOV_BOUNDS = (0.03, 0.12)

# (upper_bound, verdict) — first band whose bound >= score wins
VERDICT_BANDS = [
    (2.0, "human"),
    (4.0, "likely_human"),
    (6.0, "mixed"),
    (8.0, "likely_ai"),
    (10.0, "ai_generated"),
]

# weights sum to 1.0
FEATURE_WEIGHTS = {
    "ai_phrases": 0.18,
    "perplexity": 0.14,
    "burstiness": 0.13,
    "contraction_absence": 0.12,
    "paragraph_uniformity": 0.09,
    "repetitive_starters": 0.09,
    "passive_voice": 0.08,
    "punctuation_density": 0.08,
    "personal_voice": 0.05,
    "emoji_diversity": 0.04,
}

# raw value mapped linearly to 0..1 between (low, high)
FEATURE_THRESHOLDS = {
    "ai_phrases": (0.0, 0.02),
    "perplexity": (0.0, 1.0),
    "contraction_absence": (0.0, 1.0),
    "burstiness": (0.2, 0.6),
    "passive_voice": (0.05, 0.30),
    "punctuation_density": (0.01, 0.05),
    "paragraph_uniformity": (0.1, 0.5),
    "repetitive_starters": (0.15, 0.40),
    "personal_voice": (0.0, 0.03),
    "emoji_diversity": (0.0, 1.0),
}

# high raw value means human here, so normalisation is flipped
INVERTED_FEATURES = {"burstiness", "paragraph_uniformity", "personal_voice"}

AI_PHRASES = [
    "in conclusion", "moreover", "furthermore", "additionally",
    "it is important to note", "it is worth noting",
    "it is essential to", "one must consider",
    "in today's fast-paced world", "in today's digital age",
    "in the realm of", "the landscape of",
    "plays a crucial role", "plays a pivotal role",
    "delve into", "delve deeper", "dive deep",
    "tapestry", "multifaceted", "comprehensive guide",
    "leverage", "synergy", "paradigm shift",
    "holistic approach", "cutting-edge", "game-changer",
    "seamless integration", "robust solution",
    "it's worth mentioning", "it goes without saying",
    "needless to say", "as previously mentioned",
    "firstly", "secondly", "lastly",
    "on the one hand", "on the other hand",
    "having said that", "that being said",
    "let's explore", "let us explore",
]

# near-certain AI tells — a single hit forces AI_HARD_SIGNAL_FLOOR.
# kept narrow to avoid flooring on words humans use ("moreover", "firstly").
AI_STRONG_PHRASES = [
    "delve into", "delve deeper", "tapestry", "multifaceted",
    "paradigm shift", "game-changer", "holistic approach",
    "seamless integration", "comprehensive guide", "cutting-edge",
    "in today's fast-paced world", "in today's digital age",
    "in the realm of", "navigating the", "robust solution",
    "it is important to note", "plays a crucial role",
    "plays a pivotal role", "a testament to", "underscores the",
]

# (expanded, contracted) — favouring the expanded form leans AI
CONTRACTION_PAIRS = [
    ("it is", "it's"),
    ("do not", "don't"),
    ("does not", "doesn't"),
    ("did not", "didn't"),
    ("cannot", "can't"),
    ("can not", "can't"),
    ("will not", "won't"),
    ("would not", "wouldn't"),
    ("should not", "shouldn't"),
    ("could not", "couldn't"),
    ("is not", "isn't"),
    ("are not", "aren't"),
    ("was not", "wasn't"),
    ("were not", "weren't"),
    ("have not", "haven't"),
    ("has not", "hasn't"),
    ("had not", "hadn't"),
    ("that is", "that's"),
    ("there is", "there's"),
    ("here is", "here's"),
    ("what is", "what's"),
    ("who is", "who's"),
    ("i am", "i'm"),
    ("we are", "we're"),
    ("they are", "they're"),
    ("you are", "you're"),
    ("i have", "i've"),
    ("we have", "we've"),
    ("they have", "they've"),
    ("i will", "i'll"),
    ("we will", "we'll"),
    ("i would", "i'd"),
    ("we would", "we'd"),
    ("let us", "let's"),
]

PERSONAL_MARKERS = [
    "i think", "i believe", "in my experience", "honestly",
    "to be honest", "in my opinion", "personally",
    "i feel", "i guess", "if you ask me",
]

# casual slang/abbreviations LLMs rarely use in long-form prose — strong human tell
HUMAN_SLANG = [
    "hmu", "hit me up", "dm me", "slide into my dms", "iykyk",
    "tbh", "imo", "imho", "irl", "ngl", "fr fr", "smh",
    "idk", "idc", "lmao", "lmfao", "rofl", "afaik", "istg",
    "tldr", "tl;dr", "lowkey", "highkey", "fomo", "no cap",
    "goated", "wanna", "gonna", "gotta", "kinda", "y'all",
]

PERSONAL_PRONOUNS = ["my", "me", "we", "our", "us"]

COMMON_EMOJIS = {
    "😂", "❤️", "😍", "🤣", "😊", "🙏", "😭", "😘", "👍",
    "😅", "👏", "😁", "🔥", "🥰", "🤔", "💕", "💔", "😢", "🙌",
}
