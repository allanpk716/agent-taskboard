# 一次性原型：拼出 a/b/c.html 自包含页面。用法：sh build.sh a b c
for c in "$@"; do
  { echo '<!-- 一次性原型，不合并 -->'
    echo '<!doctype html><html lang="zh"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>工单标签筛选候选</title><style>'
    cat common.css
    echo '</style><script>'
    cat common.js
    echo '</script></head><body>'
    cat "$c.body.html"
    echo '</body></html>'
  } > "$c.html"
done
