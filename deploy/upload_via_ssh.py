#!/usr/bin/env python3
"""Upload binary via SSH using pty (no scp/paramiko needed)"""
import os, pty, select, sys, time, re

def main():
    if len(sys.argv) < 4:
        print("Usage: upload_via_ssh.py <host> <password> <local_file> <remote_path>", file=sys.stderr)
        return 1
    
    host = sys.argv[1]
    password = sys.argv[2]
    local_file = sys.argv[3]
    remote_path = sys.argv[4]
    
    if not os.path.isfile(local_file):
        print(f"Local file not found: {local_file}", file=sys.stderr)
        return 1
    
    file_size = os.path.getsize(local_file)
    print(f"Uploading {local_file} ({file_size} bytes) to {host}:{remote_path}")
    
    # 通过 ssh + base64 传输（避免 scp）
    script = f"""
set -e
base64 -d > {remote_path}.tmp
mv {remote_path}.tmp {remote_path}
chmod +x {remote_path}
ls -lh {remote_path}
echo "UPLOAD_DONE"
"""
    
    pid, master = pty.fork()
    if pid == 0:
        # child
        os.environ['TERM'] = 'dumb'
        os.execvp('ssh', ['ssh', '-T', '-o', 'StrictHostKeyChecking=no', 
                          '-o', 'UserKnownHostsFile=/dev/null',
                          f'root@{host}', f'bash -c {repr(script)}'])
        os._exit(127)
    
    # parent
    sent_password = False
    sent_data = False
    buf = b''
    start = time.time()
    
    try:
        while time.time() - start < 300:
            r, w, _ = select.select([master], [master] if not sent_data else [], [], 1.0)
            
            if master in r:
                try:
                    data = os.read(master, 65536)
                except OSError:
                    break
                if not data:
                    break
                buf += data
                sys.stdout.write(data.decode('utf-8', 'replace'))
                sys.stdout.flush()
                
                if not sent_password and re.search(rb'[Pp]assword:', buf):
                    os.write(master, (password + '\n').encode())
                    sent_password = True
            
            if master in w and sent_password and not sent_data:
                # 发送 base64 编码的文件
                print("\n[Sending file...]", file=sys.stderr)
                with open(local_file, 'rb') as f:
                    import base64
                    encoded = base64.b64encode(f.read())
                    os.write(master, encoded)
                    os.write(master, b'\n')
                sent_data = True
            
            if sent_data:
                # 等待远端完成
                pid_done, status = os.waitpid(pid, os.WNOHANG)
                if pid_done == pid:
                    # drain
                    while True:
                        try:
                            data = os.read(master, 65536)
                            if not data:
                                break
                            sys.stdout.write(data.decode('utf-8', 'replace'))
                        except OSError:
                            break
                    sys.stdout.flush()
                    return 0 if os.waitstatus_to_exitcode(status) == 0 else 1
        
        print("\n[TIMEOUT]", file=sys.stderr)
        os.kill(pid, 9)
        return 124
    except KeyboardInterrupt:
        os.kill(pid, 9)
        return 130

if __name__ == '__main__':
    sys.exit(main())
