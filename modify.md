## 修改建议

1. 首先challenge没有管理，需要增加challenge的CRUD接口和UI页面。需要先创建challenge，之后可以在challenge添加多个target和exploit。

2. exp创建逻辑不对，可以在challenge管理里面创建exp，也可以在外部，下拉菜单选择对应的challenge进行创建。创建的时候，需要上传附件和填写run.sh脚本内容。

3. Flag requeue貌似没有起作用，应该要看到attempt更新，以及log里面有重新提交的记录，可以在同一个logs中append新的log。

## 修改建议

1. exp的约定有两个文件，其中一个是文件a，另外一个是run.sh，是个run.sh具有默认模版，不修改可以按照默认。文件a是一个占位符，可以是zip也可以是py文件。举个例子，我上传一个文件a是exp.py，然后run.sh默认就是执行`python exp.py $1 $2`，如果用户不想用默认的run.sh，可以提供一个窗口修改run.sh的内容。
