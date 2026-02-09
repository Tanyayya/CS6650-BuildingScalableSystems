import re
import requests
from collections import Counter

URL = "https://raw.githubusercontent.com/teropa/nlp/master/resources/corpora/gutenberg/shakespeare-hamlet.txt"
WORD_RE = re.compile(r"[a-z0-9]+")

text = requests.get(URL).text.lower()

counts = Counter(WORD_RE.findall(text))

total_unique_words = len(counts)
total_words = sum(counts.values())

print("Total unique words:", total_unique_words)
print("Total words:", total_words)
print("Top 10 words:", counts.most_common(10))
