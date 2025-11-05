# 实现简易的flag上传检验，python实现，提供一个api，上传一个flag，校验如果flag开头是flag就返回通过，如果是不是flag就返回错误，然后允许从env中读取环境变量time和p，表示有p概率会导致延迟time秒返回

import os
import time
import random
from flask import Flask, request, jsonify

app = Flask(__name__)

# 从环境变量中读取time和p
DELAY_TIME = float(os.getenv("time", 0))  # 默认延迟时间为0秒
DELAY_PROBABILITY = float(os.getenv("p", 0))  # 默认延迟概率为0

@app.route('/upload_flag', methods=['POST'])
def upload_flag():
    data = request.json
    if not data or 'flag' not in data:
        return jsonify({"status": "error", "message": "No flag provided"}), 400

    flag = data['flag'].lower()
    if not flag.startswith("flag"):
        return jsonify({"status": "error", "message": "Invalid flag"}), 400

    # 模拟延迟逻辑
    if random.random() < DELAY_PROBABILITY:
        time.sleep(DELAY_TIME)

    return jsonify({"status": "success", "message": "Flag is valid"}), 200

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=5000)

# Example curl command for testing:
# curl -X POST -H "Content-Type: application/json" -d '{"flag": "flag123"}' http://127.0.0.1:5000/upload_flag