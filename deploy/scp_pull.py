#!/usr/bin/env python3
"""从远端拉取文件: 与 scp_auto.py 同源, 只是方向相反(SSH 通道走数据, pty 只回答密码)。

用法: python3 scp_pull.py <host> <password> <remote_path> <local_file> [timeout]
"""
import os, pty, re, select, signal, sys, time


def main() -> int:
    if len(sys.argv) < 5:
        print("usage: scp_pull.py <host> <password> <remote_path> <local_file> [timeout]", file=sys.stderr)
        return 2
    host, password, remote_path, local_file = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
    timeout = int(sys.argv[5]) if len(sys.argv) > 5 else 600
    pid, master = pty.fork()
    if pid == 0:
        os.environ["TERM"] = "dumb"
        try:
            os.execvp("scp", [
                "scp", "-q",
                "-o", "StrictHostKeyChecking=no",
                "-o", "UserKnownHostsFile=/dev/null",
                "-o", "PreferredAuthentications=password",
                "-o", "PubkeyAuthentication=no",
                "-o", "NumberOfPasswordPrompts=1",
                "root@" + host + ":" + remote_path, local_file,
            ])
        finally:
            os._exit(127)
    start = time.time()
    buf = b""
    sent = False
    status = None
    while True:
        if time.time() - start > timeout:
            try: os.kill(pid, signal.SIGKILL)
            except OSError: pass
            print("[scp_pull] timeout", file=sys.stderr); return 124
        try: ready, _, _ = select.select([master], [], [], 1.0)
        except (OSError, ValueError): break
        if master in ready:
            try: chunk = os.read(master, 4096)
            except OSError: chunk = b""
            if not chunk: break
            buf += chunk
            if not sent and re.search(rb"(password|Password|passwd)", buf):
                os.write(master, password.encode() + b"\n"); sent = True; buf = b""
        done, st = os.waitpid(pid, os.WNOHANG)
        if done: status = st; break
    if status is None:
        _, status = os.waitpid(pid, 0)
    code = os.WEXITSTATUS(status) if os.WIFEXITED(status) else 1
    if code != 0: print(f"[scp_pull] exit code: {code}", file=sys.stderr)
    return code


if __name__ == "__main__":
    sys.exit(main())
