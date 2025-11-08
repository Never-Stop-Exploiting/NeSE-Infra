from api_wraps import ControlPlaneAPI

cp = ControlPlaneAPI()

teams = [
    "perfect blue",
    "r3kapig",
    "Dragon Sector",
    "Balsn",
    "Plaid Parliament of Pwning",
    "Shellphish",
    "More Smoked Leet Chicken",
    "Eat Sleep Pwn Repeat",
    "HITCON",
    "FAUST"
]

ch1 = cp.create_challenge(name="easyweb", category="web")
ch2 = cp.create_challenge(name="happyrequest", category="web")

for team in teams:
    cp.create_target(challenge_id=ch1["id"], team_name=team, ip="192.168.66.206", port=5001, concurrency_limit=10)
    cp.create_target(challenge_id=ch2["id"], team_name=team, ip="192.168.66.206", port=5001, concurrency_limit=10)

print("Targets created successfully.")