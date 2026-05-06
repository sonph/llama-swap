#!/usr/bin/env python3
"""Llama.cpp Memory Monitor - Real-time RAM/VRAM + KV cache display.

Pulls from llama-swap /running to discover servers, then /memory and /slots
on each llama-server to visualize memory usage and KV cache utilization.

Usage:
    python3 llama_monitor.py              # continuous (2s refresh)
    python3 llama_monitor.py --once       # single shot
    python3 llama_monitor.py --port 8005  # custom llama-swap port
"""

import json
import re
import shutil
import subprocess
import sys
import time
import urllib.request
from datetime import datetime
from typing import Optional

try:
    from rich.live import Live
    from rich.text import Text
    from rich.console import Console
    rich_available = True
except ImportError:
    rich_available = False

try:
    from termcolor import colored
except ImportError:
    def colored(text: str, color: str, **_: object) -> str:
        return text


BAR_WIDTH = 22
REFRESH_INTERVAL = 2.0
DEFAULT_SWAP_PORT = 8006


def style(text: str, c: str, bold: bool = False) -> str:
    if rich_available:
        s = f"bold {c}" if bold else c
        return f"[{s}]{text}[/{s}]"
    elif rich_available is False:
        attrs = ["bold"] if bold else []
        return colored(text, c, attrs=attrs)
    return text


def stacked_bar(segments: list[tuple[float, str]], width: int = BAR_WIDTH) -> str:
    """Build a stacked bar from (fraction, color) segments.

    Each fraction is 0.0–1.0 of total bar width. Unused cells are "░".
    Non-zero segments get at least 1 cell so tiny values stay visible.
    """
    pos = 0
    bar = ""
    for frac, color in segments:
        cells = int(round(frac * width))
        if frac > 0 and cells == 0:
            cells = 1
        for _ in range(cells):
            if pos < width:
                bar += style("\u2588", color)
                pos += 1
    for _ in range(width - pos):
        bar += "\u2591"
    return bar


def legend(parts: list[tuple[str, str, str, float]], total_str: str = "") -> str:
    """Build a legend line, skipping items with value 0.

    Each part: (label, formatted_value, color, raw_value_mib).
    """
    items = " / ".join(style(f"{label} {v}", c) for label, v, c, val in parts if val > 0)
    if total_str:
        items += f" / {total_str}"
    return f"({items})"


def fetch_json(url: str, timeout: int = 3) -> Optional[dict]:
    try:
        with urllib.request.urlopen(url, timeout=timeout) as resp:
            return json.loads(resp.read())
    except Exception:
        return None


def get_system_ram() -> tuple[float, float]:
    """Get system RAM (total, used) in MiB via /proc/meminfo."""
    try:
        with open("/proc/meminfo") as f:
            info = {}
            for line in f:
                parts = line.split()
                if len(parts) >= 2:
                    info[parts[0].rstrip(":")] = int(parts[1])  # kB
        total = info.get("MemTotal", 0) / 1024
        avail = info.get("MemAvailable", 0) / 1024
        used = total - avail
        return total, used
    except Exception:
        return 0.0, 0.0


def get_gpu_utilization() -> dict[int, int]:
    """Get GPU utilization percentages via nvidia-smi.

    Returns {gpu_index: utilization_pct} for all GPUs.
    """
    try:
        result = subprocess.run(
            ["nvidia-smi", "--query-gpu=index,utilization.gpu",
             "--format=csv,noheader,nounits"],
            capture_output=True, text=True, check=True, timeout=5,
        )
        util = {}
        for line in result.stdout.strip().split("\n"):
            parts = [p.strip() for p in line.split(",")]
            if len(parts) == 2:
                util[int(parts[0])] = int(parts[1])
        return util
    except Exception:
        return {}


def fetch_slots(port: int) -> list[dict]:
    data = fetch_json(f"http://localhost:{port}/slots")
    if isinstance(data, list):
        return data
    return []


def fetch_memory(port: int) -> Optional[dict]:
    return fetch_json(f"http://localhost:{port}/memory")


def fetch_model_info(port: int) -> Optional[dict]:
    data = fetch_json(f"http://localhost:{port}/v1/models")
    if isinstance(data, dict) and data.get("models"):
        return data["models"][0]
    return None


def fetch_swap_models(port: int) -> list[dict]:
    data = fetch_json(f"http://localhost:{port}/running")
    if isinstance(data, dict):
        return data.get("running", [])
    return []


def format_size(mib: float) -> str:
    gb = mib / 1024
    if gb >= 1:
        return f"{gb:.2f}G"
    return f"{gb:.1f}G"


def _format_cells(used: int, total: int) -> str:
    """Format cell counts with k suffix, e.g. 16000/200000 -> 16/200k cells."""
    if total >= 1000:
        return f"{used // 1000}/{total // 1000}k cells"
    return f"{used}/{total} cells"


class ModelMonitor:
    """Track one llama-server instance."""

    def __init__(self, model_id: str, port: int, description: str = ""):
        self.model_id = model_id
        self.port = port
        self.description = description
        self._last_refresh = 0.0
        self._memory: Optional[dict] = None
        self._slots: list[dict] = []
        self._model_info: Optional[dict] = None
        self._gpu_utilization: dict[int, int] = {}
        self._error: Optional[str] = None

    def refresh(self) -> None:
        now = time.time()
        if now - self._last_refresh < REFRESH_INTERVAL:
            return
        self._last_refresh = now

        self._memory = fetch_memory(self.port)
        self._slots = fetch_slots(self.port)
        if self._model_info is None:
            self._model_info = fetch_model_info(self.port)
        self._gpu_utilization = get_gpu_utilization()

        if not self._memory and not self._slots:
            self._error = "unreachable"

    @property
    def name(self) -> str:
        if self._model_info:
            return self._model_info.get("name", self.model_id)
        return self.description or self.model_id

    @property
    def is_ok(self) -> bool:
        return self._error is None and (self._memory is not None or self._slots)

    def format(self, show_bars: bool = True) -> str:
        lines = []
        header = f"{self.name} (:{self.port})"
        lines.append(style(header, "cyan", bold=True))

        if not self.is_ok:
            lines.append(f"  {style('No model loaded', 'yellow')}\n")
            return "\n".join(lines)

        # Memory breakdown
        if self._memory:
            lines.append(self._format_memory(show_bars))
        else:
            lines.append(style("  /memory endpoint not available", "yellow"))

        # KV cache
        lines.append(self._format_kv_cache(show_bars))

        return "\n".join(lines) + "\n"

    def _format_memory(self, show_bars: bool) -> str:
        """Format memory breakdown with stacked bars.

        green=model, cyan=KV-cache, magenta=compute, white=other.
        """
        lines = []
        mem = self._memory
        devices = mem.get("devices", [])
        host = mem.get("host", {})

        for dev in devices:
            dev_id = dev.get("device", "?")
            desc = dev.get("description", "")
            total = dev.get("total_mib", 0)
            model_mib = dev.get("model_mib", 0)
            context_mib = dev.get("context_mib", 0)
            compute_mib = dev.get("compute_mib", 0)
            free = dev.get("free_mib", 0)
            used = total - free
            other_mib = max(0, used - model_mib - context_mib - compute_mib)

            label = f"GPU {dev_id}"
            if desc:
                label += f" {desc}"
            # Append GPU utilization if available
            try:
                gpu_idx = int(dev_id)
            except (ValueError, TypeError):
                # Parse trailing digits, e.g. "CUDA0" -> 0
                digits = ""
                for ch in reversed(dev_id):
                    if ch.isdigit():
                        digits = ch + digits
                    else:
                        break
                gpu_idx = int(digits) if digits else None
            if gpu_idx is not None:
                util = self._gpu_utilization.get(gpu_idx)
                if util is not None:
                    label += f" (Util: {util}%)"
            lines.append(f"  {style(label, 'default', bold=True)}")

            if show_bars and total > 0:
                bar = stacked_bar([
                    (other_mib / total, "dark_gray"),
                    (model_mib / total, "green"),
                    (context_mib / total, "cyan"),
                    (compute_mib / total, "magenta"),
                ])
                used_gb = used / 1024
                total_gb = total / 1024
                used_pct = (used / total * 100)
                bar_line = f"    {bar}  {used_gb:.0f}/{total_gb:.0f}GB ({used_pct:.0f}%)"
                leg = legend([
                    ("Model", format_size(model_mib), "green", model_mib),
                    ("KV", format_size(context_mib), "cyan", context_mib),
                    ("Comp", format_size(compute_mib), "magenta", compute_mib),
                ])
                lines.append(bar_line)
                lines.append(f"    {leg}")
            else:
                parts = [format_size(model_mib)]
                if context_mib > 0:
                    parts.append(format_size(context_mib))
                if compute_mib > 0:
                    parts.append(format_size(compute_mib))
                lines.append(f"    {' / '.join(parts)}  ({format_size(used)} used)")

        # System RAM
        if host:
            host_model = host.get("model_mib", 0)
            host_ctx = host.get("context_mib", 0)
            host_comp = host.get("compute_mib", 0)
            host_self = host_model + host_ctx + host_comp
            sys_total, sys_used = get_system_ram()
            other_mib = max(0, sys_used - host_self)
            lines.append(f"  {style('System RAM', 'default', bold=True)}")
            if show_bars and sys_total > 0:
                bar = stacked_bar([
                    (other_mib / sys_total, "dark_gray"),
                    (host_model / sys_total, "green"),
                    (host_ctx / sys_total, "cyan"),
                    (host_comp / sys_total, "magenta"),
                ])
                used_gb = sys_used / 1024
                total_gb = sys_total / 1024
                used_pct = (sys_used / sys_total * 100)
                bar_line = f"    {bar}  {used_gb:.0f}/{total_gb:.0f}GB ({used_pct:.0f}%)"
                leg = legend([
                    ("Model", format_size(host_model), "green", host_model),
                    ("KV", format_size(host_ctx), "cyan", host_ctx),
                    ("Comp", format_size(host_comp), "magenta", host_comp),
                ])
                lines.append(bar_line)
                lines.append(f"    {leg}")
            else:
                parts = [format_size(host_model)]
                if host_ctx > 0:
                    parts.append(format_size(host_ctx))
                if host_comp > 0:
                    parts.append(format_size(host_comp))
                lines.append(f"    {' / '.join(parts)}  ({format_size(sys_used)} used)")

        return "\n".join(lines)

    def _format_kv_cache(self, show_bars: bool) -> str:
        lines = []
        if not self._slots:
            return ""

        kv = self._memory.get("kv_cache", {}) if self._memory else {}
        cells_alloc = kv.get("cells_allocated", 0)
        cells_active = kv.get("cells_active", 0)
        util = kv.get("utilization", 0.0)
        slots_data = kv.get("slots", [])

        # Fallback: compute from /slots if /memory has no kv_cache
        if not kv and self._slots:
            total_ctx = sum(s.get("n_ctx", 0) for s in self._slots)
            used_ctx = sum(s.get("tokens_used", 0) for s in self._slots)
            cells_alloc = total_ctx
            cells_active = used_ctx
            util = (used_ctx / total_ctx) if total_ctx else 0.0
            slots_data = [
                {
                    "id": s["id"],
                    "tokens": s.get("tokens_used", 0),
                    "max_tokens": s.get("n_ctx", 0),
                }
                for s in self._slots
            ]

        lines.append(f"  {style('KV Cache', 'default', bold=True)}")

        if show_bars:
            bar = stacked_bar([(util, "green")])
            cells_str = _format_cells(cells_active, cells_alloc)
            lines.append(f"    {bar} {cells_str} ({util * 100:.0f}%)")
        else:
            cells_str = _format_cells(cells_active, cells_alloc)
            lines.append(f"    {cells_str} ({util * 100:.0f}%)")

        # Per-slot detail
        for slot in slots_data:
            sid = slot.get("id", "?")
            tokens = slot.get("tokens", 0)
            max_t = slot.get("max_tokens", 0)
            slot_util = (tokens / max_t) if max_t else 0
            if show_bars:
                slot_bar = stacked_bar([(slot_util, "green")], width=8)
                slot_cells = _format_cells(tokens, max_t)
                lines.append(f"    Slot {sid}: {slot_bar} {slot_cells} ({slot_util * 100:.0f}%)")
            else:
                slot_cells = _format_cells(tokens, max_t)
                lines.append(f"    Slot {sid}: {slot_cells}")

        return "\n".join(lines)


def main():
    is_once = len(sys.argv) > 1 and sys.argv[1] == "--once"

    # Parse optional --port flag
    swap_port = DEFAULT_SWAP_PORT
    for i, arg in enumerate(sys.argv[1:]):
        if arg == "--port" and i + 1 < len(sys.argv):
            try:
                swap_port = int(sys.argv[i + 2])
            except ValueError:
                pass

    monitors: list[ModelMonitor] = []

    def discover():
        swap = fetch_swap_models(swap_port)
        if not swap:
            return False
        monitors.clear()
        for entry in swap:
            proxy = entry.get("proxy", "")
            port_match = re.search(r":(\d+)$", proxy)
            if not port_match:
                continue
            port = int(port_match.group(1))
            model_id = entry.get("model", "?")
            desc = entry.get("description", "")
            monitors.append(ModelMonitor(model_id, port, desc))
        return True

    if not discover():
        print(style(f"Cannot reach llama-swap on port {swap_port}", "red"))
        sys.exit(1)

    def build_output(show_bars: bool) -> str:
        ts = datetime.now().strftime("%H:%M:%S")
        parts = [style(f"Llama Memory Monitor - {ts}", "bold cyan"), ""]
        for m in monitors:
            parts.append(m.format(show_bars=show_bars))
        if not monitors:
            parts.append(style("No models loaded", "yellow"))
        return "\n".join(parts)

    def fit_output(output: str) -> str:
        """Strip markup and check if output fits terminal width."""
        try:
            tw = shutil.get_terminal_size().columns
        except OSError:
            tw = 80
        plain = re.sub(r"\[/?(?:bold|dim|italic|[a-z]+)[^\]]*\]", "", output)
        plain = re.sub(r"\x1b\[[0-9;]*[mGJKH]", "", plain)
        if max((len(l) for l in plain.split("\n")), default=0) > tw:
            return build_output(show_bars=False)
        return output

    # Single-shot mode
    if is_once:
        for m in monitors:
            m.refresh()
        output = fit_output(build_output(show_bars=True))
        if rich_available:
            Console().print(Text.from_markup(output), end="")
        else:
            print(output)
        return

    # Continuous mode
    from contextlib import nullcontext
    live_ctx = Live("", screen=True, auto_refresh=False) if rich_available else nullcontext(None)

    if not rich_available:
        print("\033[2J\033[H", end="")
        sys.stdout.flush()

    with live_ctx as live:
        try:
            last_clear = time.time()
            while True:
                # Re-discover models
                discover()
                for m in monitors:
                    m.refresh()

                output = fit_output(build_output(show_bars=True))

                if live is not None:
                    live.update(Text.from_markup(output))
                    live.refresh()
                else:
                    now = time.time()
                    if now - last_clear >= 3.0:
                        print("\033[2J\033[H", end="")
                        last_clear = now
                    print("\033[H", end="")
                    print(output, end="")
                    sys.stdout.flush()

                time.sleep(REFRESH_INTERVAL)
        except KeyboardInterrupt:
            if live is None:
                print()

if __name__ == "__main__":
    main()
