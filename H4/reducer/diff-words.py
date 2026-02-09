import json, re, requests
from collections import Counter

URL = "https://raw.githubusercontent.com/teropa/nlp/master/resources/corpora/gutenberg/shakespeare-hamlet.txt"
WORD_RE = re.compile(r"[a-z0-9]+")

# baseline (single machine)
text = requests.get(URL).text.lower()
base = Counter(WORD_RE.findall(text))

# reducer output
with open("final.json") as f:
    red = json.load(f)["counts"]

red_keys = set(red.keys())
base_keys = set(base.keys())

extra = sorted(list(red_keys - base_keys))
missing = sorted(list(base_keys - red_keys))

print("Reducer unique:", len(red_keys))
print("Baseline unique:", len(base_keys))
print("Extra in reducer:", len(extra))
print("Missing from reducer:", len(missing))

print("\nSample extras:", extra[:30])
print("\nSample missing:", missing[:30])
