#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "Starting llama-swap dev server on port 8006..."
echo "  Config: ${SCRIPT_DIR}/config.dev.yaml"
echo "  Binary: ${SCRIPT_DIR}/build-dev/llama-swap"
echo ""

"${SCRIPT_DIR}/build-dev/llama-swap" \
  -config "${SCRIPT_DIR}/config.dev.yaml" \
  -listen ":8006" &
SERVER_PID=$!

# Wait for the server to be ready
echo "Waiting for server to start..."
for i in $(seq 1 30); do
  if curl -s http://localhost:8006/health > /dev/null 2>&1; then
    echo "Server is up!"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "Timed out waiting for server"
    exit 1
  fi
  sleep 1
done

# Wait an extra 5 seconds for the preload hook (gemma4-e2b) to finish loading
echo "Waiting 5s for model preload..."
sleep 5

BASE_URL="http://localhost:8006/v1/chat/completions"

# ---------- single-turn requests (varying context sizes) ----------
echo ""
echo "Sending 10 single-turn test requests..."
echo ""

# Req 1: very short prompt
curl -s -o /dev/null -w "Req 1 (short):        %{http_code}\n" "$BASE_URL" \
  -H "Content-Type: application/json" \
  -d '{"model":"fast","messages":[{"role":"user","content":"Write a short story of at least 5 sentences about a traveler."}],"max_tokens":128,"temperature":0.7}'
sleep 2

# Req 2: short
curl -s -o /dev/null -w "Req 2 (short):        %{http_code}\n" "$BASE_URL" \
  -H "Content-Type: application/json" \
  -d '{"model":"fast","messages":[{"role":"user","content":"Explain quantum entanglement in a paragraph of at least 4 sentences."}],"max_tokens":128,"temperature":0.7}'
sleep 2

# Req 3: medium (~50 tokens)
MEDIUM=$(python3 -c "print('The capital of France is Paris. ' * 8)")
curl -s -o /dev/null -w "Req 3 (medium):       %{http_code}\n" "$BASE_URL" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"fast\",\"messages\":[{\"role\":\"user\",\"content\":\"Summarize the following: $MEDIUM\"}],\"max_tokens\":32,\"temperature\":0.1}"
sleep 2

# Req 4: medium-long (~200 tokens)
MED_LONG=$(python3 -c "print('Lorem ipsum dolor sit amet, consectetur adipiscing elit. Vivamus lacinia odio vitae vestibulum vestibulum. Cras venenatis euismod malesuada. ' * 20)")
curl -s -o /dev/null -w "Req 4 (med-long):     %{http_code}\n" "$BASE_URL" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"fast\",\"messages\":[{\"role\":\"user\",\"content\":\"Read and summarize: $MED_LONG\"}],\"max_tokens\":32,\"temperature\":0.1}"
sleep 2

# Req 5: long (~1000 tokens)
LONG=$(python3 -c "print('The quick brown fox jumps over the lazy dog. ' * 100)")
curl -s -o /dev/null -w "Req 5 (long):         %{http_code}\n" "$BASE_URL" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"fast\",\"messages\":[{\"role\":\"user\",\"content\":\"Analyze this text: $LONG\"}],\"max_tokens\":32,\"temperature\":0.1}"
sleep 2

# Req 6: long (~1000 tokens)
LONG2=$(python3 -c "print('In the beginning there was nothing. Then there was more. ' * 100)")
curl -s -o /dev/null -w "Req 6 (long):         %{http_code}\n" "$BASE_URL" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"fast\",\"messages\":[{\"role\":\"user\",\"content\":\"Continue this story: $LONG2\"}],\"max_tokens\":32,\"temperature\":0.1}"
sleep 2

# Req 7: longer (~2000 tokens)
LONGER=$(python3 -c "print('This is a test of the emergency broadcasting system. This is only a test. ' * 200)")
curl -s -o /dev/null -w "Req 7 (longer):       %{http_code}\n" "$BASE_URL" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"fast\",\"messages\":[{\"role\":\"user\",\"content\":\"Summarize: $LONGER\"}],\"max_tokens\":32,\"temperature\":0.1}"
sleep 2

# Req 8: longer (~2000 tokens)
LONGER2=$(python3 -c "print('Hello world! This is a test of context size. ' * 200)")
curl -s -o /dev/null -w "Req 8 (longer):       %{http_code}\n" "$BASE_URL" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"fast\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply to: $LONGER2\"}],\"max_tokens\":32,\"temperature\":0.1}"
sleep 2

# Req 9: very long (~5000 tokens)
VERY_LONG=$(python3 -c "print('The end is near. The end is near. ' * 500)")
curl -s -o /dev/null -w "Req 9 (very long):    %{http_code}\n" "$BASE_URL" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"fast\",\"messages\":[{\"role\":\"user\",\"content\":\"Process: $VERY_LONG\"}],\"max_tokens\":32,\"temperature\":0.1}"
sleep 2

# Req 10: very long (~5000 tokens)
VERY_LONG2=$(python3 -c "print('Data! Data! Data! I cannot make bricks without clay. ' * 500)")
curl -s -o /dev/null -w "Req 10 (very long):   %{http_code}\n" "$BASE_URL" \
  -H "Content-Type: application/json" \
  -d "{\"model\":\"fast\",\"messages\":[{\"role\":\"user\",\"content\":\"Explain: $VERY_LONG2\"}],\"max_tokens\":32,\"temperature\":0.1}"

echo ""
echo "Single-turn requests complete!"

# ---------- multi-turn conversation (context accumulates per turn) ----------
echo ""
echo "Sending multi-turn conversation (6 turns, context grows each turn)..."
echo ""

python3 - "$BASE_URL" <<'PYEOF'
import subprocess, json, sys

base_url = sys.argv[1]
turn_prompts = [
    "Start a very detailed sci-fi story. Introduce two characters on a space station. Describe the setting in rich detail, at least one paragraph each.",
    "Write an elaborate response from the first character. They should explain the history of this station in great detail, mentioning at least 10 named facilities, each with a description of at least 3 sentences.",
    "Now write the second character's reply. They should respond to each of the 10 facilities with their own memories and assessments. Each assessment should be 2-3 sentences long.",
    "Write another detailed turn from the first character. They should share a detailed report about a malfunction they discovered. Describe at least 12 specific systems, each with a paragraph-length description of the problem.",
    "Second character responds with a repair plan. For each of the 12 systems, propose a solution. Each solution should be a paragraph with at least 4 sentences describing the approach, risks, and required materials.",
    "Write the final turn where both characters discuss the outcome. Summarize all 12 systems and whether the repairs worked. Each system should get 2-3 sentences of outcome description.",
]

history = []

for i, prompt in enumerate(turn_prompts, 1):
    history.append({"role": "user", "content": prompt})
    body = json.dumps({"model": "fast", "messages": history, "max_tokens": 128, "temperature": 0.7}).encode()
    result = subprocess.run(
        ["curl", "-s", base_url, "-H", "Content-Type: application/json",
         "-d", body.decode()], capture_output=True, text=True
    )
    resp = json.loads(result.stdout)
    usage = resp.get("usage", {})
    prompt_tokens = usage.get("prompt_tokens", "?")
    print(f"Turn {i} (prompt_tokens={prompt_tokens}): OK")

    # Grab assistant reply for the next turn
    if i < len(turn_prompts):
        assistant_text = resp["choices"][0]["message"]["content"]
        history.append({"role": "assistant", "content": assistant_text})
PYEOF

echo ""
echo "Multi-turn conversation complete!"
echo ""
echo "Dev server running (PID $SERVER_PID). Press Ctrl+C to stop."
wait "$SERVER_PID"
