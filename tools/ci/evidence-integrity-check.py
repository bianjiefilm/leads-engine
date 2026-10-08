#!/usr/bin/env python3
"""EIG-B1: 证据引用完整性门 —— docs/audits 内引用的仓内文件路径逐一对比 git 追踪集。

用法:
  evidence-integrity-check.py <repo-path> [ref] [--scope GLOB] [--only-added BASE]
                                          [--also OTHER-REPO-PATH]...
                                          [--cross-names a,b,c]

默认 ref=origin/main,scope=docs/audits。对 ref 先 git fetch 再跑。
只读,不改任何数据;可直接作 CI step(退出码即门)。
--also: 跨仓解析面(产品仓文档合法引用 public-ai 路径等场景),可重复传。

解析规则(按 2026-10-09 首轮实测的误报类别调校):
  - token 形态: 裸路径 / 反引号 / markdown 链接;扩展名白名单;剥 :N #锚点
  - 跳过: http(s)/mailto、8+位纯 hex(SHA)、'/'开头(路由式/绝对路径叙事)、
    '-'开头或含'...'的片段、'.'开头的隐藏/截断片段
  - 解析顺序: ①仓根相对 ②相对引用文件目录(经 normpath) ③目录引用(祖先目录集)
    ③.5 仓名前缀式引用(剥离与 --also 仓名/主仓名相同的前导段后按目标仓解析)
    ④仓内唯一同名松解析(endswith('/'+raw) 恰好一个命中,处理「axe-summary.json」
    这类省略子目录的简写引用;命中记 loose)
    ⑤跨仓唯一同名松解析(--also 各仓自己的 origin/main)
    ⑤.5 后缀松解析(唯一后缀命中: 'lock.json' 省略前缀简写;多命中=weak)
    ⑤.6 生成物 weak: *.lock.json(staged 外部交付)/两段各自带不同扩展名
    (app.js/app.json 这类产物列举)
    ⑤.7 结构性 weak: .env 结尾(凭据按红线不入库)/含纯数字段(home-390/1440/1920.png
    这类三档简写)/无'/'且词基是常见代码符号(console.log 等)
  - pinned 引用类(不进普通 token 流,按指针自带定位逐条验证):
    repo@ref:path → 在 alias 仓 cat-file -e ref:path
    repo worktree[dir]:path → 磁盘存在性(显式 dir / alias 仓本体+其父目录+注册 worktree)
    失效=pinned 指针悬空(清单+exit 1);别名未配置=weak
  - 悬空分级: strong=清单+exit 1;weak=散文式提及,只警告不拦截
  - --only-added BASE: 只扫 BASE..ref 新增文件(新证据包从严,历史文档不追溯)
  - --cross-names a,b,c: CI 轻量模式——无 --also 跨仓面时,仓名前缀式引用降级
    weak 不拦截(跨仓核验留在 root 全量跑);仓内悬空仍 strong

背景(三连事故,均为「证据包=把运行产物拷进 docs/audits」撞仓级 gitignore):
  hui-2626 0-png 虚惊 / hui-2623 诊断宿主抢救 / hui-2628 两份 e2e-run.log 真吞(PR#44 回录)。

退出码: 0=无 strong 悬空  1=存在 strong 悬空(打印清单)  2=错误
"""
import os
import re
import subprocess
import sys

EXT = (r'(?:log|png|jpg|jpeg|gif|webp|md|json|txt|mjs|cjs|ts|tsx|js|jsx|'
       r'go|uvue|uts|sh|py|ya?ml|csv|snap|env|lock)')
TOKEN = re.compile(r'[\w.\-/()]+' + r'\.' + EXT + r'\b')
MDLINK = re.compile(r'\]\(([^)#\s]+)\)')
HEXISH = re.compile(r'^[0-9a-f]{8,40}$', re.I)
WEAKBASE = re.compile(r'^[A-Z][A-Za-z]*$')
# 无'/'且词基为这些常见代码符号的 token(console.log 等)不是文件引用
CODESYM = {'console', 'window', 'document', 'process', 'require', 'module',
           'global', 'import', 'export', 'navigator', 'localStorage'}
# 只扫文本类文件(PNG 等二进制 decode 后会产生伪 token)
TEXT_EXT = {'.md', '.json', '.txt', '.mjs', '.cjs', '.ts', '.tsx', '.js',
            '.jsx', '.go', '.py', '.sh', '.yaml', '.yml', '.csv', '.env',
            '.uvue', '.uts', '.lock', '.snap'}  # 不含 .log: 日志是原始产物,内容里的构建路径不是引用


def sh(repo, *args):
    r = subprocess.run(['git', '-C', repo] + list(args), capture_output=True)
    # 二进制文件(png 等)会混进扫描面,字节捕获 + replace 解码避免崩溃
    return subprocess.CompletedProcess(
        r.args, r.returncode,
        stdout=r.stdout.decode('utf-8', errors='replace'),
        stderr=r.stderr.decode('utf-8', errors='replace'))


def main(argv):
    if len(argv) < 2:
        print(__doc__)
        return 2
    repo = argv[1]
    rest = argv[2:]
    ref, scope, base = 'origin/main', 'docs/audits', None
    alsos = []
    cross_names = set()
    while rest:
        a = rest.pop(0)
        if a == '--scope' and rest:
            scope = rest.pop(0)
        elif a == '--only-added' and rest:
            base = rest.pop(0)
        elif a == '--also' and rest:
            alsos.append(rest.pop(0))
        elif a == '--cross-names' and rest:
            cross_names = {n.strip().lower() for n in rest.pop(0).split(',') if n.strip()}
        elif not a.startswith('--'):
            ref = a
    if sh(repo, 'rev-parse', '--verify', ref).returncode != 0:
        print('!! ref 不存在: %s (先 git fetch origin)' % ref)
        return 2

    if base:
        diff = sh(repo, 'diff', '--name-only', '--diff-filter=A', base, ref, '--', scope)
        if diff.returncode != 0:
            print('!! diff 失败: %s' % diff.stderr[:300])
            return 2
        files = [f for f in diff.stdout.splitlines() if f]
    else:
        ls = sh(repo, 'ls-tree', '-r', '--name-only', ref, '--', scope)
        files = [f for f in ls.stdout.splitlines() if f]
    files = [f for f in files if os.path.splitext(f)[1].lower() in TEXT_EXT]
    if not files:
        print('scope 下无可扫文件: %s (ref=%s%s)' % (scope, ref, ', base=%s' % base if base else ''))
        return 0

    tracked = set(sh(repo, 'ls-tree', '-r', '--name-only', ref).stdout.splitlines())
    dirset = set()
    for t in tracked:
        parts = t.split('/')
        for i in range(1, len(parts)):
            dirset.add('/'.join(parts[:i]))
    # 松解析索引: basename → [tracked 路径]
    by_base = {}
    for t in tracked:
        by_base.setdefault(t.rsplit('/', 1)[-1], []).append(t)
    # 跨仓解析面(--also,可重复): 名称 → (tracked, dirset, by_base)
    # 一律取各仓自己的 origin/main(跨仓引用问的是「上游现在有没有」,目标 ref 只对主仓有意义)
    others = []
    for a in alsos:
        ot = set(sh(a, 'ls-tree', '-r', '--name-only', 'origin/main').stdout.splitlines())
        od = set()
        for t in ot:
            parts = t.split('/')
            for i in range(1, len(parts)):
                od.add('/'.join(parts[:i]))
        ob = {}
        for t in ot:
            ob.setdefault(t.rsplit('/', 1)[-1], []).append(t)
        others.append((os.path.basename(a.rstrip('/')), ot, od, ob))
        print('[evidence-check] --also 装载 %s: %d 个 tracked 路径 (origin/main)' % (os.path.basename(a.rstrip('/')), len(ot)), file=sys.stderr)

    # ── pinned 引用(repo@ref:path / repo worktree[dir]:path)──
    # MANIFEST 类证据指针:定位信息写在指针里,不进普通 token 流
    PINNED_REF = re.compile(r'([\w.-]+)@([^:\s]+):([\w.\-/()]+\.' + EXT + r')')
    PINNED_WT = re.compile(r'([\w.-]+)\s+worktree(?:\s+([^\s:]+))?:([\w.\-/()]+\.' + EXT + r')')
    alias_map = {os.path.basename(a.rstrip('/')): a for a in alsos}
    wt_cache = {}
    pinned_seen, pinned_paths, pinned_dangling = set(), set(), []
    pinned_ok_n = [0]

    def verify_pinned(alias, rref, wtdir, ppath, citing):
        key = (alias, rref, wtdir, ppath)
        if key in pinned_seen:
            return
        pinned_seen.add(key)
        pinned_paths.add(ppath)
        ok = False
        if rref is not None:
            ar = alias_map.get(alias)
            if ar and sh(ar, 'cat-file', '-e', '%s:%s' % (rref, ppath)).returncode == 0:
                ok = True
            elif not ar:
                weak.setdefault(ppath, []).append('pinned 别名未在 --also 配置: %s' % alias)
                return
        else:
            cands = []
            if wtdir:
                cands.append(wtdir if os.path.isabs(wtdir) else os.path.join(os.getcwd(), wtdir))
            elif alias in alias_map:
                ar = alias_map[alias]
                cands += [ar, os.path.dirname(ar.rstrip('/'))]
                if ar not in wt_cache:
                    wl = sh(ar, 'worktree', 'list', '--porcelain')
                    wt_cache[ar] = [l.split(' ', 1)[1] for l in wl.stdout.splitlines()
                                    if l.startswith('worktree ')]
                cands += wt_cache[ar]
            ok = any(os.path.exists(os.path.join(b, ppath)) for b in cands if b)
        if ok:
            pinned_ok_n[0] += 1
        else:
            loc = '%s@%s:%s' % (alias, rref, ppath) if rref is not None \
                else '%s worktree%s:%s' % (alias, (' ' + wtdir) if wtdir else '', ppath)
            pinned_dangling.append((loc, citing))


    # 逐文件取内容(git show),文件数有限,比全量 grep 更省且天然支持 --only-added
    strong, weak, loose = {}, {}, []
    cand_n = ok_n = 0
    seen = set()

    def resolve(raw, citedir):
        # ①② 本仓精确: 仓根相对 / 相对引用文件目录 / 目录引用
        for c in (raw, os.path.normpath(os.path.join(citedir, raw)) if citedir else os.path.normpath(raw)):
            c = c[2:] if c.startswith('./') else c
            if c in tracked or c in dirset:
                return 'ok', None
        # ③ 跨仓精确(--also)
        for name, ot, od, _ob in others:
            for c in (raw, os.path.normpath(os.path.join(citedir, raw)) if citedir else os.path.normpath(raw)):
                c = c[2:] if c.startswith('./') else c
                if c in ot or c in od:
                    return 'ok', '%s:%s' % (name, c)
        # ③.5 仓名前缀式引用(shuhai-guanlan/backend/...、GoBoost/GoBoost/internal/...):
        # 剥掉与仓名(或主仓名,大小写不敏感,可重复)相同的前导段后在对应仓解析
        main_name = os.path.basename(repo.rstrip('/'))
        alias_names = {name.lower() for name, _ot, _od, _ob in others}
        segs = raw.split('/')
        strip_n = 0
        while len(segs) > 1 and segs[0].lower() in (alias_names | {main_name.lower()}):
            segs.pop(0)
            strip_n += 1
        if strip_n:
            stripped = '/'.join(segs)
            if stripped in tracked or stripped in dirset:
                return 'ok', None
            for name, ot, od, _ob in others:
                if stripped in ot or stripped in od:
                    return 'ok', '%s:%s' % (name, stripped)
            sn = stripped.rsplit('/', 1)[-1]
            xsub = [t for t in sum([ob.get(sn, []) for _n, _o, _d, ob in others], []) if t.endswith('/' + stripped)]
            if len(xsub) == 1:
                return 'loose', xsub[0]
        base_name = raw.rsplit('/', 1)[-1]
        # ④ 本仓松解析(唯一同名)
        sub = [t for t in by_base.get(base_name, []) if t.endswith('/' + raw)]
        if len(sub) == 1:
            return 'loose', sub[0]
        if len(sub) > 1:
            return 'weak', '本仓 %d 处同名歧义' % len(sub)
        # ⑤ 跨仓松解析(唯一同名)
        for name, _ot, _od, ob in others:
            xsub = [t for t in ob.get(base_name, []) if t.endswith('/' + raw)]
            if len(xsub) == 1:
                return 'loose', '%s:%s' % (name, xsub[0])
            if len(xsub) > 1:
                return 'weak', '%s %d 处同名歧义' % (name, len(xsub))
        # ⑤.5 后缀松解析(唯一后缀命中): 'lock.json' 这类省略前缀的简写
        all_paths = tracked
        ssub = [t for t in all_paths if t.rsplit('/', 1)[-1].endswith(raw) and t != raw]
        for _n, ot, _od, _ob in others:
            ssub += [t for t in ot if t.rsplit('/', 1)[-1].endswith(raw)]
        if len(ssub) == 1:
            return 'loose', ssub[0]
        if len(ssub) > 1:
            return 'weak', '%d 处后缀同名歧义' % len(ssub)
        # ⑤.6 生成物/符号列举 weak
        if base_name.endswith('.lock.json'):
            return 'weak', None
        parts2 = raw.split('/')
        if len(parts2) == 2 and all(re.fullmatch(r'[\w-]+\.[a-z]+', p) for p in parts2) \
                and parts2[0].rsplit('.', 1)[1] != parts2[1].rsplit('.', 1)[1]:
            return 'weak', None
        # ⑤.7 结构性 weak
        base_noext = base_name.rsplit('.', 1)[0]
        if raw.endswith('.env'):
            return 'weak', None
        if any(seg.isdigit() for seg in raw.split('/')):
            return 'weak', None
        if '/' not in raw and base_noext in CODESYM:
            return 'weak', None
        # ⑥ 散文式大写单词(Next.js 类)→ weak
        if '/' not in raw and WEAKBASE.fullmatch(base_noext):
            return 'weak', None
        # ⑦ 含括号的散文引用(如「组合(resolved.json」)→ weak
        if '(' in raw or ')' in raw:
            return 'weak', None
        # ⑧ CI 轻量模式:仓名前缀式引用在无 --also 面时降级 weak(跨仓核验归 root 全量跑)
        if cross_names and raw.split('/', 1)[0].lower() in cross_names:
            return 'weak', None
        return 'dangling', '无任何解析命中'

    show_fail = 0
    docs = []
    for path in files:
        r = sh(repo, 'show', '%s:%s' % (ref, path))
        if r.returncode != 0:
            show_fail += 1
            continue
        docs.append((path, os.path.dirname(path), r.stdout))

    # pass 1: pinned 引用先收全(其路径要从 pass 2 的普通 token 流里排除)
    for path, _citedir, text in docs:
        for m in PINNED_REF.finditer(text):
            verify_pinned(m.group(1), m.group(2), None, m.group(3), path)
        for m in PINNED_WT.finditer(text):
            verify_pinned(m.group(1), None, m.group(2), m.group(3), path)

    # pass 2: 普通 token 流
    for path, citedir, text in docs:
        raws = [m.group(1) for m in MDLINK.finditer(text)]
        raws += TOKEN.findall(text)
        for raw in raws:
            if raw.startswith(('http://', 'https://', 'mailto:')):
                continue
            if HEXISH.fullmatch(raw):
                continue
            if raw.startswith(('/', '-', '.')) or '...' in raw:
                continue
            # markdown 链接被 TOKEN 连括号捕获的情形:平衡括号才剥;
            # 另:裸左括号(ext 前回退丢右括号所致)直接剥——仓内无以'('开头的真实路径
            if raw.startswith('(') and raw.endswith(')') and raw.count('(') == raw.count(')'):
                raw = raw[1:-1]
                if not raw:
                    continue
            while raw.startswith('('):
                raw = raw[1:]
            if raw in pinned_paths:
                continue  # 已按 pinned 指针验证,不重复计
            key = (raw, citedir)
            if key in seen:
                continue
            seen.add(key)
            cand_n += 1
            verdict, note = resolve(raw, citedir)
            if verdict == 'ok':
                ok_n += 1
            elif verdict == 'loose':
                ok_n += 1
                loose.append((raw, note))
            elif verdict == 'weak':
                weak.setdefault(raw, []).append(path)
            else:
                strong.setdefault(raw, []).append((path, note))

    print('repo=%s ref=%s scope=%s%s' % (
        os.path.basename(repo.rstrip('/')), ref, scope,
        ', only-added=%s' % base if base else ''))
    print('扫描文件=%d  引用候选(去重)=%d  在库=%d(含 loose %d)  '
          'weak=%d  strong 悬空=%d  pinned 验真=%d 失效=%d' % (
              len(files) - show_fail, cand_n, ok_n, len(loose),
              len(weak), len(strong), pinned_ok_n[0], len(pinned_dangling)))
    if loose:
        print('\n-- loose(简写引用,仓内唯一解析)示例,最多 8 条 --')
        for raw, full in loose[:8]:
            print('  %s → %s' % (raw, full))
    if weak:
        print('\n-- weak 警告(散文式提及,不拦截),最多 10 条 --')
        for i, (raw, cites) in enumerate(sorted(weak.items())):
            if i >= 10:
                break
            print('  %s  ← %s' % (raw, cites[0]))
    if pinned_dangling:
        print('\n-- PINNED 指针失效(repo@ref / worktree 磁盘定位均无此物) --')
        for loc, citing in pinned_dangling:
            print('  %s\n      ← %s' % (loc, citing))
    if strong:
        print('\n-- STRONG 悬空清单(最多 50 条) --')
        for i, (raw, cites) in enumerate(sorted(strong.items())):
            if i >= 50:
                print('  ... 其余 %d 项略' % (len(strong) - 50))
                break
            print('  %s\n      ← %s (%s)' % (raw, cites[0][0], cites[0][1]))
        return 1
    if pinned_dangling:
        return 1
    print('\nOK 无 strong 悬空引用(证据完整性门通过)')
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv))
