from api_wraps import ControlPlaneAPI
import os

os.environ["CONTROL_PLANE_TOKEN"] = "dev-admin-token"

cp = ControlPlaneAPI()

teams = [
    "来自东方的神秘力量",
    "NeverStopExploiting",
    "Nu1L",
    "天枢Dubhe",
    "W_M",
    "0x300R",
    "A1Gipsy",
    "Redbud",
    "Lilac",
    "eee",
    "0x401",
    "Polaris",
    "N0wayBack",
    "S1uM4i",
    "Again",
    "皖电卫士",
    "Xp0int",
    "粤电粤安全",
    "BOI",
    "SN-天虞",
    "Dawn",
    "W4terDr0p",
    "联通轩辕战队",
    "天命",
    "Nepnep",
    "SGSec",
    "NISA",
    "Novaxis",
    "Flux-Become-ACT",
    "Spirit",
    "XUPTSEC",
    "SanDieg0"
]

ch1 = cp.create_challenge(name="easyweb", category="web")
ch2 = cp.create_challenge(name="happyrequest", category="web")
ch3 = cp.create_challenge(name="easypwn", category="pwn")

for team in teams:
    cp.create_target(challenge_id=ch1["id"], team_name=team, ip="192.168.66.206", port=5001, concurrency_limit=10)
    cp.create_target(challenge_id=ch2["id"], team_name=team, ip="192.168.66.206", port=5001, concurrency_limit=10)
    cp.create_target(challenge_id=ch3["id"], team_name=team, ip="192.168.66.206", port=49999, concurrency_limit=10)

print("Targets created successfully.")