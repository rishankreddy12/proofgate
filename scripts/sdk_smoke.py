import os
import subprocess
import sys
import time

def main():
    db_url = os.getenv("DATABASE_URL", "postgres://proofgate:proofgate@localhost:15432/proofgate?sslmode=disable")
    os.environ["DATABASE_URL"] = db_url

    tenant = f"sdk-smoke-{int(time.time())}"
    go_cmd = "go"
    if os.name == "nt":
        prog_go = r"C:\Program Files\Go\bin\go.exe"
        if os.path.exists(prog_go):
            go_cmd = prog_go

    subprocess.run([go_cmd, "run", "./cmd/proofgatectl", "tenant", "create", "--name", tenant], check=True, stdout=subprocess.DEVNULL)
    res = subprocess.run([go_cmd, "run", "./cmd/proofgatectl", "key", "create", "--tenant", tenant, "--name", "sdk"], check=True, capture_output=True, text=True)
    key = res.stdout.strip()
    os.environ["PROOFGATE_KEY"] = key

    script_dir = os.path.dirname(os.path.abspath(__file__))
    sdk_smoke_path = os.path.join(script_dir, "..", "e2e", "sdk_smoke.py")
    subprocess.run([sys.executable, sdk_smoke_path], check=True)

if __name__ == "__main__":
    main()
