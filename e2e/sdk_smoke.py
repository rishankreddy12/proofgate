"""Proves drop-in compatibility: the official OpenAI Python SDK talks to ProofGate unchanged."""
import os
import sys

from openai import OpenAI

base_url = os.getenv("PROOFGATE_BASE_URL", "http://localhost:18080/v1")
client = OpenAI(base_url=base_url, api_key=os.environ["PROOFGATE_KEY"])

resp = client.chat.completions.create(model="default", messages=[{"role": "user", "content": "hello"}], max_tokens=3)
assert resp.choices[0].message.content.startswith("echo: hello"), resp

text = ""
for chunk in client.chat.completions.create(
    model="default", messages=[{"role": "user", "content": "hi"}], max_tokens=3, stream=True,
    stream_options={"include_usage": True},
):
    if chunk.choices:
        text += chunk.choices[0].delta.content or ""
    elif chunk.usage:
        assert chunk.usage.completion_tokens == 3, chunk.usage
assert text == "echo: hi lorem", text

emb = client.embeddings.create(model="embed", input=["a b", "c"])
assert len(emb.data) == 2

print("SDK smoke OK")
sys.exit(0)
