for c in "$@"; do
  {
    echo '<!doctype html><html lang="zh"><head>'
    echo '<!-- 一次性原型（#91 交接文案入口），不合并 -->'
    echo '<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>交接文案入口候选</title><style>'
    cat common.css
    echo '</style></head><body>'
    cat "$c.body.html"
    echo '<script>'
    cat common.js
    echo 'render()</script></body></html>'
  } > "$c.html"
done
