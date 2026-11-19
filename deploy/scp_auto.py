#!/usr/bin/env python3
"""纯标准库 scp 自动化: pty 只用于密码提示, 文件数据走 SSH 通道。

用法: python3 scp_auto.py <host> <password> <local_file> <remote_path> [timeout]
"""
import os, pty, re, select, signal, sys, time


def main() -> int:
    if len(sys.argv) < 5:
        print("usage: scp_auto.py <host> <password> <local_file> <remote_path> [timeout]", file=sys.stderr)
        return 2
    host, password, local_file, remote_path = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
    timeout = int(sys.argv[5]) if len(sys.argv) > 5 else 300

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
                local_file, "root@" + host + ":" + remote_path,
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
            if not sent and len(buf) < 10000 and re.search(rb"[Pp]assword\s*:?", buf):
                os.write(master, (password + "\n").encode())
                sent = True
        else:
            p, st = os.waitpid(pid, os.WNOHANG)
            if p == pid:
                status = st
                break

    if status is None:
        try:
            p, st = os.waitpid(pid, os.WNOHANG)
            if p == pid:
                status = st
        except OSError:
            pass
    if timed_out:
        print("\n[scp_auto] TIMEOUT", file=sys.stderr)
        return 124
    if status is None:
        try:
            os.kill(pid, signal.SIGKILL)
            os.waitpid(pid, 0)
        except OSError:
            pass
        return 125
    code = os.waitstatus_to_exitcode(status)
    print("[scp_auto] exit code: %d" % code)
    return code


if __name__ == "__main__":
    sys.exit(main())
