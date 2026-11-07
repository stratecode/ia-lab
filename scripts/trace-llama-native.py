#!/usr/bin/env python3
"""Persist sampled native inference boundaries without prompts or token data.

This observer attaches uprobes to the installed llama.cpp libraries. It never
changes the model process. Each received event is written with O_DSYNC so a
hard power loss leaves the last delivered checkpoint on disk when the storage
device honors flushes.
"""

import datetime
import json
import os
import pathlib
import signal
import subprocess
import sys


TRACE_PATH = pathlib.Path("/var/log/lab-investigation/native-inference-steps.jsonl")
BOOT_PATH = pathlib.Path("/proc/sys/kernel/random/boot_id")
BPF_PROGRAM = r"""
BEGIN {
  printf("observer.ready|0|0|%llu|0|0|0\n", nsecs);
}
uprobe:/opt/llama.cpp/build/bin/libllama.so.0:llama_decode {
  @decode_seq[pid] = @decode_seq[pid] + 1;
  @decode_start[tid] = nsecs;
  if (@decode_seq[pid] <= 8 || (@decode_seq[pid] & 15) == 0) {
    @decode_report[tid] = @decode_seq[pid];
    printf("decode.enter|%d|%d|%llu|%llu|0|0\n", pid, tid, nsecs, @decode_seq[pid]);
  }
}
uretprobe:/opt/llama.cpp/build/bin/libllama.so.0:llama_decode /@decode_start[tid]/ {
  if (@decode_report[tid]) {
    printf("decode.exit|%d|%d|%llu|%llu|%llu|%d\n", pid, tid, nsecs,
           @decode_report[tid], nsecs - @decode_start[tid], retval);
  }
  delete(@decode_start[tid]);
  delete(@decode_report[tid]);
}
uprobe:/opt/llama.cpp/build/bin/libggml-base.so.0:ggml_backend_sched_graph_compute_async {
  @compute_seq[pid] = @compute_seq[pid] + 1;
  @compute_start[tid] = nsecs;
  if (@compute_seq[pid] <= 8 || (@compute_seq[pid] & 15) == 0) {
    @compute_report[tid] = @compute_seq[pid];
    printf("compute.enter|%d|%d|%llu|%llu|0|0\n", pid, tid, nsecs, @compute_seq[pid]);
  }
}
uretprobe:/opt/llama.cpp/build/bin/libggml-base.so.0:ggml_backend_sched_graph_compute_async /@compute_start[tid]/ {
  if (@compute_report[tid]) {
    printf("compute.exit|%d|%d|%llu|%llu|%llu|%d\n", pid, tid, nsecs,
           @compute_report[tid], nsecs - @compute_start[tid], retval);
  }
  delete(@compute_start[tid]);
  delete(@compute_report[tid]);
}
"""


def record(fd, boot_id, fields):
    event, pid, tid, monotonic_ns, sequence, duration_ns, result = fields
    payload = {
        "at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "boot_id": boot_id,
        "event": event,
        "pid": int(pid),
        "tid": int(tid),
        "monotonic_ns": int(monotonic_ns),
        "sequence": int(sequence),
        "duration_ns": int(duration_ns),
        "result": int(result),
    }
    data = (json.dumps(payload, separators=(",", ":")) + "\n").encode()
    while data:
        written = os.write(fd, data)
        if written == 0:
            raise OSError("native trace write returned zero bytes")
        data = data[written:]


def main():
    if sys.argv[1:] == ["--print-bpf"]:
        print(BPF_PROGRAM)
        return 0

    TRACE_PATH.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    boot_id = BOOT_PATH.read_text().strip()
    flags = os.O_CREAT | os.O_APPEND | os.O_WRONLY | os.O_DSYNC
    fd = os.open(TRACE_PATH, flags, 0o600)
    os.fchmod(fd, 0o600)
    process = subprocess.Popen(
        ["/usr/bin/bpftrace", "-q", "-B", "none", "-e", BPF_PROGRAM],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        bufsize=1,
    )
    stopping = False

    def stop(_signal, _frame):
        nonlocal stopping
        stopping = True
        process.terminate()

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    try:
        for line in process.stdout:
            fields = line.strip().split("|")
            if len(fields) == 7 and fields[0] in {
                "observer.ready", "decode.enter", "decode.exit", "compute.enter", "compute.exit"
            }:
                try:
                    record(fd, boot_id, fields)
                except ValueError:
                    print("native_trace_invalid_event", file=sys.stderr)
            elif line.strip():
                print(line.rstrip()[:500], file=sys.stderr)
        code = process.wait()
        if not stopping:
            print(f"native_trace_observer_exited code={code}", file=sys.stderr)
        return 0 if stopping else 1
    finally:
        os.close(fd)


if __name__ == "__main__":
    raise SystemExit(main())
