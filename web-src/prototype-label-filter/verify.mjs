// 一次性原型验证脚本，不合并。
import { chromium } from 'file:///C:/Users/allan716/AppData/Roaming/npm/node_modules/@playwright/cli/node_modules/playwright/index.mjs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import fs from 'node:fs'

const root = path.dirname(fileURLToPath(import.meta.url))
const shots = path.join(root, 'shots')
fs.mkdirSync(shots, { recursive: true })

// 独立副本：只保留验证规则需要的字段，避免调用页面过滤函数。
const verifyTickets = [
  { labels: ['机型:UD-2300','系统:液路','科室:检验科','待初筛'], closed: false },
  { labels: ['机型:UD-2300','系统:液路','科室:急诊','紧急'], closed: false },
  { labels: ['机型:UD-2300','系统:液路','科室:检验科','产品线:血液','售后'], closed: false },
  { labels: ['机型:UD-2300','系统:液路','科室:检验科','待初筛'], closed: false },
  { labels: ['机型:UD-2300','系统:软件','科室:检验科','固件'], closed: false },
  { labels: ['机型:UD-2300','系统:软件','产品线:尿液','已复现'], closed: false },
  { labels: ['机型:UD-2300','科室:门诊化验'], closed: false },
  { labels: ['机型:UD-2300','系统:质控','科室:检验科','产品线:血液','售后'], closed: false },
  { labels: ['机型:UD-2300','系统:光路','科室:病理科','已复现'], closed: false },
  { labels: ['机型:UD-2300','系统:条码','科室:急诊'], closed: false },
  { labels: ['机型:UD-1600','系统:液路','科室:检验科'], closed: false },
  { labels: ['机型:UD-1600','系统:液路','科室:门诊化验','产品线:尿液','待初筛'], closed: false },
  { labels: ['机型:UD-1600','系统:软件','科室:检验科','售后'], closed: false },
  { labels: ['机型:UD-1600','系统:软件','科室:急诊','紧急'], closed: false },
  { labels: ['机型:UD-1600','系统:打印','科室:门诊化验','固件'], closed: false },
  { labels: ['机型:UD-1600','系统:质控','科室:检验科'], closed: false },
  { labels: ['机型:UD-1600','系统:光路','科室:病理科'], closed: false },
  { labels: ['机型:UD-3100','系统:液路','科室:急诊','产品线:血液','紧急'], closed: false },
  { labels: ['机型:UD-3100','系统:软件','科室:检验科','产品线:尿液','售后'], closed: false },
  { labels: ['机型:UD-3100','系统:打印','科室:检验科'], closed: false },
  { labels: ['机型:BH-7600CS','系统:液路','科室:检验科','产品线:尿液'], closed: false },
  { labels: ['机型:BH-7600CS','系统:软件','科室:急诊','紧急'], closed: false },
  { labels: ['机型:BH-7600CS','系统:打印','科室:门诊化验'], closed: false },
  { labels: ['机型:BH-7600CS','系统:质控','科室:检验科','产品线:尿液','售后'], closed: false },
  { labels: ['机型:UD-900','系统:打印','科室:体检中心','产品线:血液','固件'], closed: false },
  { labels: ['机型:BH-5800','系统:软件','科室:体检中心','产品线:尿液','已复现'], closed: false },
  { labels: ['机型:BH-5800','科室:急诊','产品线:尿液'], closed: false },
  { labels: ['产品线:尿液'], closed: false },
  { labels: ['售后'], closed: true },
  { labels: ['紧急','已复现'], closed: true },
]

function dimOf(label) {
  const i = label.indexOf(':')
  return i < 0 ? '其他标签' : label.slice(0, i)
}
function independentCount(filter) {
  const groups = {}
  for (const label of filter.inc) (groups[dimOf(label)] ??= []).push(label)
  return verifyTickets.filter(t => {
    if (t.closed) return false
    return Object.values(groups).every(values => values.some(v => t.labels.includes(v))) &&
      filter.exc.every(v => !t.labels.includes(v))
  }).length
}

const expectedMulti = independentCount({ inc: ['机型:UD-2300','机型:UD-1600','系统:液路'], exc: [] })
const expectedExclude = independentCount({ inc: ['机型:UD-2300','机型:UD-1600','系统:液路'], exc: ['待初筛'] })
const candidates = ['a', 'b', 'c']
const scenes = [['empty','空'], ['multi','多选'], ['exclude','排除'], ['open','已展开']]
const widths = [1366, 1024, 390]
const errors = []
const assertions = []
let screenshotCount = 0

check('multi count within required range', expectedMulti >= 5 && expectedMulti <= 7, `expected=${expectedMulti}`)

function check(name, ok, detail = '') {
  assertions.push(`${ok ? 'PASS' : 'FAIL'} ${name}${detail ? ` | ${detail}` : ''}`)
  if (!ok) process.exitCode = 1
}

const browser = await chromium.launch({
  executablePath: 'C:/Users/allan716/AppData/Local/ms-playwright/chromium_headless_shell-1248/chrome-headless-shell-win64/chrome-headless-shell.exe',
})

for (const width of widths) {
  const context = await browser.newContext({ viewport: { width, height: width === 390 ? 844 : 900 } })
  for (const candidate of candidates) {
    const page = await context.newPage()
    page.on('console', message => { if (message.type() === 'error') errors.push(`${candidate}/${width}: ${message.text()}`) })
    page.on('pageerror', error => errors.push(`${candidate}/${width}: ${error.message}`))
    await page.goto('file:///' + path.join(root, `${candidate}.html`).replaceAll('\\', '/'))
    await page.locator('#bar button').filter({ hasText: String(width === 390 ? '手机' : width) }).first().click()
    await page.waitForTimeout(60)

    for (const [scene, label] of scenes) {
      await page.locator('#bar button').filter({ hasText: label }).first().click()
      await page.waitForTimeout(100)
      await page.screenshot({ path: path.join(shots, `${candidate}-${scene}-${width}.png`), fullPage: true })
      screenshotCount++
    }

    await page.locator('#bar button').filter({ hasText: '多选' }).first().click()
    const multiText = await page.locator('.lcount').first().innerText()
    const multiCount = Number(multiText.replace(/\D/g, ''))
    check(`${candidate}/${width} multi count`, multiCount === expectedMulti, `page=${multiCount}, expected=${expectedMulti}`)

    await page.locator('#bar button').filter({ hasText: '排除' }).first().click()
    const excludeText = await page.locator('.lcount').first().innerText()
    const excludeCount = Number(excludeText.replace(/\D/g, ''))
    check(`${candidate}/${width} exclude count`, excludeCount === expectedExclude, `page=${excludeCount}, expected=${expectedExclude}`)
    check(`${candidate}/${width} exclude narrows`, excludeCount <= multiCount, `exclude=${excludeCount}, multi=${multiCount}`)

    if (width === 1024 || width === 390) {
      const overflow = await page.evaluate(() => {
        const docs = document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1
        const app = document.getElementById('pc').scrollWidth <= document.getElementById('pc').clientWidth + 1
        const list = document.querySelector('.lcol, .mobile')
        const listOk = !list || list.scrollWidth <= list.clientWidth + 1
        return { docs, app, listOk, dw: document.documentElement.scrollWidth, dc: document.documentElement.clientWidth, aw: document.getElementById('pc').scrollWidth, ac: document.getElementById('pc').clientWidth }
      })
      check(`${candidate}/${width} no horizontal overflow`, overflow.docs && overflow.app && overflow.listOk, JSON.stringify(overflow))
    }

    if (width === 1366) {
      await page.locator('#bar button').filter({ hasText: '空' }).first().click()
      const chip = page.locator('.lbody .chip.dim').first()
      const label = await chip.innerText()
      await chip.click()
      const selected = await page.locator('.schips').innerText()
      check(`${candidate} row chip adds condition`, selected.includes(label.split(':').pop()), `label=${label}`)
    }
    await page.close()
  }
  await context.close()
}

await browser.close()
check('no JS or console errors', errors.length === 0, errors.join(' | '))
console.log(`screenshots=${screenshotCount}`)
console.log(`expected multi=${expectedMulti}, exclude=${expectedExclude}`)
for (const assertion of assertions) console.log(assertion)
