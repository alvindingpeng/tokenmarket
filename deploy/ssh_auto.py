#!/usr/bin/env python3
"""纯标准库 SSH 自动化: 用 pty.fork 模拟 expect, 无需 sshpass/paramiko。

用法: python3 ssh_auto.py <host> <password> <script_file> [timeout_seconds]
脚本文件内容会作为远端 shell 命令一次性执行。
"""
import os, pty, re, select, signal, sys, time


def main() -> int:
    if len(sys.argv) < 4:
        print("usage: ssh_auto.py <host> <password> <script_file> [timeout]", file=sys.stderr)
        return 2
    host, password, script_file = sys.argv[1], sys.argv[2], sys.argv[3]
    timeout = int(sys.argv[4]) if len(sys.argv) > 4 else 300

    with open(script_file, "r", encoding="utf-8") as f:
        script = f.read()

    pid, master = pty.fork()
    if pid == 0:
        os.environ["TERM"] = "dumb"
        try:
            os.execvp("ssh", [
                "ssh", "-T",
                "-o", "StrictHostKeyChecking=no",
                "-o", "UserKnownHostsFile=/dev/null",
                "-o", "PreferredAuthentications=password",
                "-o", "PubkeyAuthentication=no",
                "-o", "NumberOfPasswordPrompts=1",
                "-o", "ConnectTimeout=15",
                "root@" + host,
                script,
            ])
        finally:
            os._exit(127)

    start = time.time()
    buf = b""
    sent = False
    status = None
    timed_out = False

    while True:
        if time.time() - start > timeout:
            timed_out = True
            try:
                os.kill(pid, signal.SIGKILL)
            except OSError:
                pass
            break
        try:
            ready, _, _ = select.select([master], [], [], 1.0)
        except (OSError, ValueError):
            break
        if master in ready:
            try:
                data = os.read(master, 65536)
            except OSError:
                break
            if not data:
                break
            buf += data
            sys.stdout.write(data.decode("utf-8", "replace"))
            sys.stdout.flush()
            if not sent and len(buf) < 20000 and re.search(rb"[Pp]assword\s*:?", buf):
                os.write(master, (password + "\n").encode())
                sent = True
        else:
            p, st = os.waitpid(pid, os.WNOHANG)
            if p == pid:
                status = st
                break

    for _ in range(20):
        try:
            ready, _, _ = select.select([master], [], [], 0.2)
        except (OSError, ValueError):
            break
        if master not in ready:
            break
        try:
            data = os.read(master, 65536)
        except OSError:
            break
        if not data:
            break
        sys.stdout.write(data.decode("utf-8", "replace"))
        sys.stdout.flush()

    if status is None:
        try:
            p, st = os.waitpid(pid, os.WNOHANG)
            if p == pid:
                status = st
        except OSError:
            pass

    if timed_out:
        print("\n[ssh_auto] TIMEOUT after %ds" % timeout, file=sys.stderr)
        return 124
    if status is None:
        try:
            os.kill(pid, signal.SIGKILL)
            os.waitpid(pid, 0)
        except OSError:
            pass
        return 125
    code = os.waitstatus_to_exitcode(status)
    print("\n[ssh_auto] remote exit code: %d" % code)
    return code


if __name__ == "__main__":
    sys.exit(main())
