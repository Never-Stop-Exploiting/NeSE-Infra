# 实现简易的flag上传检验，python实现，提供一个api，上传一个flag，校验如果flag开头是flag就返回通过，如果是不是flag就返回错误，然后允许从env中读取环境变量time和p，表示有p概率会导致延迟time秒返回

import os
import time
import random
from flask import Flask, request, jsonify

app = Flask(__name__)

@app.route('/flag', methods=['GET'])
def upload_flag():
    flag = request.args.get('flag', '')
    return jsonify({"status": "success", "flag": flag}), 200

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=5000)

# Example curl command for testing:
# curl -X POST -H "Content-Type: application/json" -d '{"flag": "flag123"}' http://127.0.0.1:5000/upload_flag