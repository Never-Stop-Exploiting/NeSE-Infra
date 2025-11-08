import requests
import sys
import time
import random

HOST = sys.argv[1]
PORT = sys.argv[2]

print(f"Start testing challenge upload to {HOST}:{PORT}", flush=True)
time.sleep(0.5)
print("Sending request...", flush=True)

flags = [
    "flag{test}", "ctf{2222}"
]

r = requests.get(f"http://{HOST}:{PORT}/flag?flag={random.choice(flags)}")

print(r.status_code, flush=True)
flag = r.json().get("flag")

if random.randint(0, 1) == 0:
    print(r.text, flush=True)
else:
    with open("flag", "w") as f:
        f.write(flag)
