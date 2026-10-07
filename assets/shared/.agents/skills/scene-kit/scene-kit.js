// scene-kit：HyperFrames 镜头的按帧驱动工具库（缓动、淡入淡出、安全区、字幕）。
//
// ============================ 三条硬约束 ============================
//
// 1) 一切取值都是「帧号 -> 画面」的纯函数。渲染机按任意帧 seek，不保证顺序、
//    不保证只走一遍；本库不读时钟、不用未播种随机数、不建定时器、不发网络请求，
//    也不在两次 render 之间携带任何影响画面的状态。
//
// 2) 帧号一律是镜头本地帧（第 0 帧是本镜头第一帧），与 am-scene.js 里 cues /
//    phrases 的坐标系一致。时间换帧号只有 frameOf 一处：Math.round(秒 × fps)。
//    取整方式在两处不一致时，字幕会与配音慢慢错开，而且看半天说不出哪里不对。
//
// 3) 数据只从 window.AM_SCENE 读。它由 am 在渲染前写进镜头目录的 am-scene.js，
//    来源是 frame.md 与本镜头的字幕时间轴。不要把安全区像素、字号、字幕时间
//    手抄进合成——frame.md 改了，手抄的那份不会跟着改。
//
// ====================================================================
(function (global) {
  'use strict';

  var SCHEMA = 1;

  // ======================= 数学 =======================

  function clamp(value, lo, hi) {
    return Math.min(hi, Math.max(lo, value));
  }

  function lerp(a, b, t) {
    return a + (b - a) * t;
  }

  var BACK = 1.70158;
  var EASES = {
    linear: function (t) { return t; },
    sineIn: function (t) { return 1 - Math.cos((t * Math.PI) / 2); },
    sineOut: function (t) { return Math.sin((t * Math.PI) / 2); },
    sineInOut: function (t) { return -(Math.cos(Math.PI * t) - 1) / 2; },
    quadIn: function (t) { return t * t; },
    quadOut: function (t) { return 1 - (1 - t) * (1 - t); },
    quadInOut: function (t) { return t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2; },
    cubicIn: function (t) { return t * t * t; },
    cubicOut: function (t) { return 1 - Math.pow(1 - t, 3); },
    cubicInOut: function (t) { return t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2; },
    expoIn: function (t) { return t <= 0 ? 0 : Math.pow(2, 10 * t - 10); },
    expoOut: function (t) { return t >= 1 ? 1 : 1 - Math.pow(2, -10 * t); },
    expoInOut: function (t) {
      if (t <= 0) return 0;
      if (t >= 1) return 1;
      return t < 0.5 ? Math.pow(2, 20 * t - 10) / 2 : (2 - Math.pow(2, -20 * t + 10)) / 2;
    },
    backIn: function (t) { return (BACK + 1) * t * t * t - BACK * t * t; },
    backOut: function (t) { return 1 + (BACK + 1) * Math.pow(t - 1, 3) + BACK * Math.pow(t - 1, 2); },
    backInOut: function (t) {
      var c = BACK * 1.525;
      return t < 0.5
        ? (Math.pow(2 * t, 2) * ((c + 1) * 2 * t - c)) / 2
        : (Math.pow(2 * t - 2, 2) * ((c + 1) * (t * 2 - 2) + c) + 2) / 2;
    },
  };

  // ease(name) 返回缓动函数；ease(name, t) 直接求值（t 先钳到 [0, 1]）。
  // 未知名字直接抛错：拼错名字静默退回 linear，画面只是「没那么好看」，没人会发现。
  function ease(name, t) {
    var fn = EASES[name];
    if (!fn) {
      throw new Error('SceneKit.ease：未知缓动 "' + name + '"，可选：' + Object.keys(EASES).join(', '));
    }
    if (t === undefined) return fn;
    return fn(clamp(t, 0, 1));
  }

  // progress：frame 在 [startFrame, endFrame] 区间里的进度，钳在 [0, 1]。
  function progress(frame, startFrame, endFrame) {
    if (endFrame <= startFrame) return frame >= startFrame ? 1 : 0;
    return clamp((frame - startFrame) / (endFrame - startFrame), 0, 1);
  }

  // fade：inStart→inEnd 从 0 升到 1，outStart→outEnd 从 1 降到 0；
  // 省略 outStart / outEnd 表示不淡出。
  function fade(frame, inStart, inEnd, outStart, outEnd) {
    var value = progress(frame, inStart, inEnd);
    if (outStart !== undefined && outStart !== null) {
      var end = outEnd === undefined || outEnd === null ? outStart : outEnd;
      value = Math.min(value, 1 - progress(frame, outStart, end));
    }
    return value;
  }

  // rng：mulberry32 固定种子伪随机数，返回一个每次调用给出 [0, 1) 的函数。
  // 种子可以是整数或字符串（字符串先做 FNV-1a 哈希）。同一种子永远给出同一序列；
  // 要让某个元素的随机取值不依赖调用顺序，按元素各开一个 rng(元素 id)。
  function rng(seed) {
    var a;
    if (typeof seed === 'string') {
      a = 2166136261;
      for (var i = 0; i < seed.length; i++) {
        a ^= seed.charCodeAt(i);
        a = Math.imul(a, 16777619);
      }
    } else {
      a = Number(seed) || 0;
    }
    a = a >>> 0;
    return function () {
      a = (a + 0x6d2b79f5) >>> 0;
      var t = a;
      t = Math.imul(t ^ (t >>> 15), t | 1);
      t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }

  // ======================= 数据 =======================

  function scene() {
    var data = global.AM_SCENE;
    if (!data || typeof data !== 'object') {
      throw new Error(
        'am-scene.js 未加载：window.AM_SCENE 不存在。am-scene.js 由 am 写在镜头目录里，' +
          '必须在 scene-kit.js 之前用 <script src="am-scene.js"></script> 引入'
      );
    }
    if (data.schema !== SCHEMA) {
      throw new Error('am-scene.js 的 schema 是 ' + data.schema + '，本版 scene-kit 只认 ' + SCHEMA + '：重新从技能目录复制 scene-kit.js');
    }
    return data;
  }

  // frameOf：时间轴秒数 -> 本地帧号。全库唯一的换算点。
  function frameOf(seconds) {
    return Math.round(seconds * scene().fps);
  }

  function phrase(id) {
    var list = scene().phrases || [];
    for (var i = 0; i < list.length; i++) {
      if (list[i].phrase_id === id) return list[i];
    }
    var known = list.map(function (p) { return p.phrase_id; });
    throw new Error('SceneKit.phrase：本镜头没有短语 "' + id + '"，已有：' + (known.join(', ') || '（无）'));
  }

  function phraseActive(id, frame) {
    var p = phrase(id);
    return frame >= p.start_frame && frame < p.end_frame_exclusive;
  }

  // sortedCues：按开始帧排序（同帧按 index），不改原数组。
  function sortedCues(cues) {
    return cues.slice().sort(function (a, b) {
      return a.start_frame - b.start_frame || a.index - b.index;
    });
  }

  // cueAt：frame 落在哪条字幕的原始区间里；区间重叠时开始得晚的那条胜出。
  function cueAt(frame) {
    var cues = sortedCues(scene().cues || []);
    var hit = null;
    for (var i = 0; i < cues.length; i++) {
      if (cues[i].start_frame <= frame && frame < cues[i].end_frame_exclusive) hit = cues[i];
    }
    return hit;
  }

  // ======================= 版式 =======================

  // safeRect：安全区在画布上的矩形。left/top/right/bottom 都是距画布左上角的
  // 坐标（right = left + width），不是 frame.md 里那种「距各边的内缩量」。
  function safeRect(name) {
    var s = scene();
    var area = s.safe_area && s.safe_area[name];
    if (!area) {
      var known = Object.keys(s.safe_area || {});
      throw new Error('SceneKit.safeRect：没有名为 "' + name + '" 的安全区，已有：' + (known.join(', ') || '（无）'));
    }
    var left = area.left_px;
    var top = area.top_px;
    var right = s.width_px - area.right_px;
    var bottom = s.height_px - area.bottom_px;
    return { left: left, top: top, right: right, bottom: bottom, width: right - left, height: bottom - top };
  }

  // ======================= 断行 =======================
  //
  // 宽度以「全角字」计：汉字与全角标点算 1，ASCII 算 0.5。断行只发生在原子块
  // 之间，原子块是：一个汉字、一个英文词（含连字符、小数点等，GPT-4、3.5、98%）、
  // 数字连同紧跟的单位（3.5 秒、12 帧）、以及《》“”""「」括起来的整段。

  var OPEN_QUOTES = { '《': '》', '“': '”', '"': '"', '「': '」', '『': '』', '‘': '’' };
  // 不能出现在行首的标点：挂到前一个原子块上。
  var NO_LINE_START = '，。、；：！？）》」』”’…—,.;:!?)]%‰';
  // 不能出现在行尾的标点：挂到后一个原子块上。
  var NO_LINE_END = '（《「『“‘([';
  // 在其后断行最自然的标点。
  var STRONG_BREAK = '；，、。！？：;,!?';
  var UNIT = /^ ?(?:毫秒|分钟|小时|公里|千米|厘米|毫米|公斤|千克|万亿|亿|万|千|百|秒|分|时|天|周|年|月|日|号|个|次|倍|元|块|岁|米|克|斤|度|帧|层|步|条|项|名|位|%|‰|℃)/;
  var ASCII_WORD = /^[A-Za-z0-9][A-Za-z0-9.\-_+/'%&#@]*/;

  function isAscii(ch) {
    return ch.charCodeAt(0) < 0x80;
  }

  function widthOf(text) {
    var w = 0;
    for (var i = 0; i < text.length; i++) w += isAscii(text[i]) ? 0.5 : 1;
    return w;
  }

  // tokenize：把一句字幕切成原子块 [{text, breakAfter}]。breakAfter 是在该块
  // 之后断行的代价：0 = 标点后，1.5 = 空格处（空格本身断行时丢弃），4 = 其余。
  function tokenize(text) {
    var raw = [];
    var i = 0;
    while (i < text.length) {
      var ch = text[i];
      if (ch === ' ' || ch === '　') {
        raw.push({ text: ' ', space: true });
        while (i < text.length && (text[i] === ' ' || text[i] === '　')) i++;
        continue;
      }
      var close = OPEN_QUOTES[ch];
      if (close) {
        var end = text.indexOf(close, i + 1);
        if (end > i) {
          raw.push({ text: text.slice(i, end + 1) });
          i = end + 1;
          continue;
        }
      }
      var word = ASCII_WORD.exec(text.slice(i));
      if (word) {
        var chunk = word[0];
        // 末尾的句点、逗号属于标点而不是词本身（"done." 里的点）。
        while (chunk.length > 1 && /[.\-_+/']$/.test(chunk)) chunk = chunk.slice(0, -1);
        var after = text.slice(i + chunk.length);
        if (/^[0-9][0-9.,]*$/.test(chunk)) {
          var unit = UNIT.exec(after);
          if (unit) chunk += unit[0].replace(/ $/, '');
        }
        raw.push({ text: chunk });
        i += chunk.length;
        continue;
      }
      raw.push({ text: ch });
      i++;
    }

    // 标点禁则：行首禁用的并进前一块，行尾禁用的并进后一块。
    var merged = [];
    var carry = '';
    for (var k = 0; k < raw.length; k++) {
      var tok = raw[k];
      if (tok.space) {
        if (carry) { merged.push({ text: carry }); carry = ''; }
        merged.push(tok);
        continue;
      }
      var t = carry + tok.text;
      carry = '';
      if (t.length === 1 && NO_LINE_END.indexOf(t) >= 0) {
        carry = t;
        continue;
      }
      var prev = merged[merged.length - 1];
      if (NO_LINE_START.indexOf(t[0]) >= 0 && prev && !prev.space) {
        prev.text += t;
        continue;
      }
      merged.push({ text: t });
    }
    if (carry) merged.push({ text: carry });

    // 空格之前的块在空格处断；行尾是强标点的块在其后断。
    var tokens = [];
    for (var m = 0; m < merged.length; m++) {
      var cur = merged[m];
      if (cur.space) {
        if (tokens.length) tokens[tokens.length - 1].trailingSpace = true;
        continue;
      }
      var last = cur.text[cur.text.length - 1];
      tokens.push({
        text: cur.text,
        width: widthOf(cur.text),
        cost: STRONG_BREAK.indexOf(last) >= 0 ? 0 : 4,
        trailingSpace: false,
      });
    }
    for (var n = 0; n < tokens.length; n++) {
      if (tokens[n].trailingSpace && tokens[n].cost > 0) tokens[n].cost = 1.5;
    }
    return tokens;
  }

  // splitOversized：单个原子块比一行还宽时只能逐字拆开（宁可拆，也不溢出画面）。
  function splitOversized(tokens, cap) {
    var out = [];
    for (var i = 0; i < tokens.length; i++) {
      var tok = tokens[i];
      if (tok.width <= cap) { out.push(tok); continue; }
      for (var j = 0; j < tok.text.length; j++) {
        var last = j === tok.text.length - 1;
        out.push({
          text: tok.text[j],
          width: widthOf(tok.text[j]),
          cost: last ? tok.cost : 6,
          trailingSpace: last && tok.trailingSpace,
        });
      }
    }
    return out;
  }

  function lineText(tokens, from, to) {
    var s = '';
    for (var i = from; i < to; i++) {
      s += tokens[i].text;
      if (i < to - 1 && tokens[i].trailingSpace) s += ' ';
    }
    return s;
  }

  function lineWidth(tokens, from, to) {
    var w = 0;
    for (var i = from; i < to; i++) {
      w += tokens[i].width;
      if (i < to - 1 && tokens[i].trailingSpace) w += 0.5;
    }
    return w;
  }

  // breakLines：在 cap（每行全角字数上限）与 maxLines 之内求最均衡的断法。
  // 代价 = Σ(行宽 - 平均行宽)² + 各断点代价；行数越少越好（多一行加罚）。
  // 返回字符串数组；放不进 maxLines 时返回 null。
  function breakLines(text, cap, maxLines) {
    var tokens = splitOversized(tokenize(text), cap);
    var n = tokens.length;
    if (n === 0) return [''];
    var total = lineWidth(tokens, 0, n);
    if (total <= cap) return [lineText(tokens, 0, n)];
    var best = null;
    for (var lines = 2; lines <= maxLines; lines++) {
      var target = total / lines;
      // dp[k][i]：前 i 个原子块排成 k 行的最小代价。
      var dp = [];
      var from = [];
      for (var k = 0; k <= lines; k++) {
        dp.push(new Array(n + 1).fill(Infinity));
        from.push(new Array(n + 1).fill(-1));
      }
      dp[0][0] = 0;
      for (var row = 1; row <= lines; row++) {
        for (var end = 1; end <= n; end++) {
          for (var start = row - 1; start < end; start++) {
            if (dp[row - 1][start] === Infinity) continue;
            var w = lineWidth(tokens, start, end);
            if (w > cap) continue;
            var c = dp[row - 1][start] + (w - target) * (w - target) + (end < n ? tokens[end - 1].cost : 0);
            if (c < dp[row][end]) {
              dp[row][end] = c;
              from[row][end] = start;
            }
          }
        }
      }
      if (dp[lines][n] === Infinity) continue;
      var score = dp[lines][n] + (lines - 2) * 8;
      if (!best || score < best.score) {
        var out = [];
        var e = n;
        for (var r = lines; r >= 1; r--) {
          var s = from[r][e];
          out.unshift(lineText(tokens, s, e));
          e = s;
        }
        best = { score: score, lines: out };
      }
    }
    return best ? best.lines : null;
  }

  // greedyLines：溢出兜底，按行塞满，不限行数。只在 breakLines 放不下时用。
  function greedyLines(text, cap) {
    var tokens = splitOversized(tokenize(text), cap);
    var out = [];
    var start = 0;
    for (var i = 1; i <= tokens.length; i++) {
      if (i > start + 1 && lineWidth(tokens, start, i) > cap) {
        out.push(lineText(tokens, start, i - 1));
        start = i - 1;
      }
    }
    if (start < tokens.length) out.push(lineText(tokens, start, tokens.length));
    return out.length ? out : [''];
  }

  // ======================= 字幕 =======================

  function px(value) {
    return value + 'px';
  }

  // layoutCue：给一条字幕定字号与断行。先用首选字号；行数超限时逐像素缩到
  // font_size_px.min；仍放不下才溢出（行数超过 max_lines），并返回 overflow。
  //
  // 字号缩小时每行可容纳的字数按比例放宽（行的像素长度保持不变），同时不超过
  // 字幕框的物理宽度。
  function layoutCue(text, cfg) {
    for (var size = cfg.fontSize; size >= cfg.minFontSize; size--) {
      var cap = Math.min(Math.floor((cfg.charsPerLine * cfg.fontSize) / size + 1e-9), Math.floor(cfg.innerWidth / size));
      var lines = breakLines(text, cap, cfg.maxLines);
      if (lines && lines.length * size * cfg.lineHeight + cfg.padV * 2 <= cfg.maxHeight + 1e-6) {
        return { fontSize: size, lines: lines, overflow: false };
      }
    }
    var minSize = cfg.minFontSize;
    var minCap = Math.max(1, Math.min(Math.floor((cfg.charsPerLine * cfg.fontSize) / minSize + 1e-9), Math.floor(cfg.innerWidth / minSize)));
    return { fontSize: minSize, lines: greedyLines(text, minCap), overflow: true };
  }

  // captions：在 root 里建一层字幕，返回 { element, layouts, render(frame) }。
  //
  // render(frame) 是纯函数：任意帧、任意顺序调用，结果只由 frame 决定。
  // 交接规则：后一条字幕一出现就截断前一条，同一时刻只显示一条；
  // leadFrames / tailFrames 可以让字幕提前出现 / 延后消失，但不侵入相邻字幕
  // 本来的显示区间。
  function captions(root, options) {
    if (!root || typeof root.appendChild !== 'function') {
      throw new Error('SceneKit.captions：第一个参数必须是挂字幕层的 DOM 元素（通常是合成根节点）');
    }
    var opts = options || {};
    var s = scene();
    var tokens = s.subtitles;
    if (!tokens) {
      throw new Error('SceneKit.captions：am-scene.js 里 subtitles 为 null（frame.md 没有字幕规范），不能按规范排字幕');
    }
    var area = safeRect(opts.safeArea || 'subtitles');
    var fontRange = tokens.font_size_px || {};
    var maxFont = opts.fontSize || fontRange.max || fontRange.min || 40;
    var minFont = Math.min(fontRange.min || maxFont, maxFont);
    var lhRange = tokens.line_height || {};
    var lineHeight = opts.lineHeight || lhRange.max || lhRange.min || 1.4;
    var pad = tokens.padding_px || {};
    var padV = pad.vertical || 0;
    var padH = pad.horizontal || 0;
    var boxWidth = Math.min(tokens.max_width_px || area.width, area.width);
    var cfg = {
      fontSize: maxFont,
      minFontSize: minFont,
      charsPerLine: tokens.max_fullwidth_chars_per_line || Math.floor((boxWidth - padH * 2) / maxFont),
      innerWidth: boxWidth - padH * 2,
      maxLines: tokens.max_lines || 2,
      lineHeight: lineHeight,
      padV: padV,
      maxHeight: area.height,
    };

    var cues = sortedCues(opts.cues || s.cues || []);
    var lead = Math.max(0, opts.leadFrames || 0);
    var tail = Math.max(0, opts.tailFrames || 0);

    // 预先排好每条字幕。溢出告警在这里按字幕序号各发一次：发在 render 里会
    // 随 seek 次数重复，告警条数就取决于渲染顺序了。
    var layouts = [];
    var maxLineCount = 1;
    for (var i = 0; i < cues.length; i++) {
      var cue = cues[i];
      var layout = layoutCue(String(cue.text || ''), cfg);
      var prevEnd = i > 0 ? cues[i - 1].end_frame_exclusive : -Infinity;
      var nextStart = i < cues.length - 1 ? cues[i + 1].start_frame : Infinity;
      layouts.push({
        cue: cue,
        fontSize: layout.fontSize,
        lines: layout.lines,
        overflow: layout.overflow,
        from: Math.max(cue.start_frame - lead, Math.min(prevEnd, cue.start_frame)),
        until: Math.min(cue.end_frame_exclusive + tail, Math.max(nextStart, cue.end_frame_exclusive)),
      });
      maxLineCount = Math.max(maxLineCount, layout.lines.length);
      if (layout.overflow && typeof console !== 'undefined' && console.warn) {
        console.warn(
          '[scene-kit] 字幕 #' + cue.index + ' 缩到最小字号 ' + minFont + 'px 仍超过 ' + cfg.maxLines +
            ' 行（实际 ' + layout.lines.length + ' 行）：请把这条字幕拆短。内容：' + cue.text
        );
      }
    }

    // DOM 只建一次；render 只改文字、字号与显隐。
    var doc = root.ownerDocument || global.document;
    var layer = doc.createElement('div');
    layer.className = 'scene-kit-captions' + (opts.className ? ' ' + opts.className : '');
    layer.setAttribute('aria-hidden', 'true');
    var align = opts.align || 'bottom';
    var ls = layer.style;
    ls.position = 'absolute';
    ls.left = px(area.left);
    ls.top = px(area.top);
    ls.width = px(area.width);
    ls.height = px(area.height);
    ls.display = 'flex';
    ls.flexDirection = 'column';
    ls.alignItems = 'center';
    ls.justifyContent = align === 'top' ? 'flex-start' : align === 'center' ? 'center' : 'flex-end';
    ls.pointerEvents = 'none';
    ls.zIndex = String(opts.zIndex === undefined ? 50 : opts.zIndex);

    var box = doc.createElement('div');
    box.className = 'scene-kit-caption';
    var bs = box.style;
    bs.boxSizing = 'border-box';
    bs.maxWidth = px(boxWidth);
    bs.padding = px(padV) + ' ' + px(padH);
    bs.borderRadius = px(tokens.radius_px || 0);
    bs.background = tokens.background || 'transparent';
    bs.color = tokens.text_color || '#FFFFFF';
    bs.lineHeight = String(lineHeight);
    bs.textAlign = 'center';
    if (opts.fontFamily) bs.fontFamily = opts.fontFamily;
    if (opts.fontWeight) bs.fontWeight = String(opts.fontWeight);
    layer.appendChild(box);

    var lineEls = [];
    for (var j = 0; j < maxLineCount; j++) {
      var line = doc.createElement('div');
      line.className = 'scene-kit-caption-line';
      line.style.whiteSpace = 'nowrap';
      box.appendChild(line);
      lineEls.push(line);
    }
    root.appendChild(layer);

    // activeAt：frame 所在的显示窗口，开始得晚的窗口截断开始得早的。
    function activeAt(frame) {
      var hit = null;
      for (var k = 0; k < layouts.length; k++) {
        if (layouts[k].from <= frame && frame < layouts[k].until) hit = layouts[k];
      }
      return hit;
    }

    // 隐藏时也清空文字、复位字号：DOM 的每一项都只由 frame 决定，
    // 不留上一次 render 的残迹（即使看不见，截图对比与自检读 DOM 时也会读到）。
    function render(frame) {
      var current = activeAt(frame);
      var lines = current ? current.lines : [];
      ls.visibility = current ? 'visible' : 'hidden';
      bs.fontSize = px(current ? current.fontSize : maxFont);
      for (var k = 0; k < lineEls.length; k++) {
        var text = k < lines.length ? lines[k] : '';
        if (lineEls[k].textContent !== text) lineEls[k].textContent = text;
        lineEls[k].style.display = k < lines.length ? 'block' : 'none';
      }
      return current ? current.cue : null;
    }

    render(0);
    return { element: layer, layouts: layouts, render: render, activeAt: activeAt };
  }

  // ======================= 接入 GSAP 时间线 =======================
  //
  // HyperFrames 渲染时 seek 那条 paused timeline。只靠 onUpdate 不够：GSAP 的
  // tl.seek(t) 默认 suppressEvents=true，onUpdate 一次都不触发；宿主若走
  // window.__hf.seek / window.__player.seek，子时间线自己的 seek 也不会被调用。
  // 做法照上游官方示例（hyperframes-animation/examples/messaging-multi-phrase.html）
  // 与本项目 character-rig 的 cast.js：
  //   1) 一条覆盖整镜的驱动 tween，onUpdate 里出帧；
  //   2) timeline 自己的 seek / pause 包一层，调用后补出一帧；
  //   3) window.__hf / window.__player 的 seek 包一层，补出全部已接入的帧。
  // 出帧时间一律取 timeline 自己的 time()，不用 seek 的入参（宿主可能按毫秒传）。

  var attached = [];

  function drawAll() {
    for (var i = 0; i < attached.length; i++) attached[i]();
  }

  function wrapSeek(owner, after) {
    if (!owner || typeof owner.seek !== 'function') return;
    var inner = owner.seek;
    owner.seek = function () {
      var result = inner.apply(this, arguments);
      after();
      return result;
    };
  }

  // hookWindow：宿主对象可能在本库之后才赋值，所以用访问器等它出现。已有访问器
  // （例如 cast.js 先装了一层）时串在它后面，不覆盖它。
  function hookWindow(key) {
    var flag = '__sceneKitHooked_' + key;
    if (global[flag]) return;
    global[flag] = true;
    try {
      if (global[key]) {
        wrapSeek(global[key], drawAll);
        return;
      }
      var previous = Object.getOwnPropertyDescriptor(global, key);
      var pending;
      Object.defineProperty(global, key, {
        configurable: true,
        get: function () {
          return previous && previous.get ? previous.get.call(global) : pending;
        },
        set: function (value) {
          if (previous && previous.set) previous.set.call(global, value);
          else pending = value;
          wrapSeek(previous && previous.get ? previous.get.call(global) : value, drawAll);
        },
      });
    } catch (_) {
      /* 宿主不让改就算了，驱动 tween 才是主路径 */
    }
  }

  // attach(timeline, render)：把 render(帧号) 挂到一条 paused GSAP 时间线上，
  // 返回一个无参的 draw()，调用它会按时间线当前位置补出一帧。
  function attach(timeline, render) {
    if (!timeline || typeof timeline.to !== 'function' || typeof timeline.time !== 'function') {
      throw new Error('SceneKit.attach：第一个参数必须是 gsap.timeline({ paused: true }) 建出的时间线');
    }
    if (typeof render !== 'function') {
      throw new Error('SceneKit.attach：第二个参数必须是 render(frame) 函数');
    }
    var s = scene();
    var lastFrame = Math.max(0, s.frames - 1);
    function draw() {
      render(clamp(frameOf(timeline.time()), 0, lastFrame));
    }
    timeline.to({ t: 0 }, { t: 1, duration: s.frames / s.fps, ease: 'none', onUpdate: draw }, 0);
    wrapSeek(timeline, draw);
    if (typeof timeline.pause === 'function') {
      var pause = timeline.pause;
      timeline.pause = function () {
        var result = pause.apply(this, arguments);
        draw();
        return result;
      };
    }
    attached.push(draw);
    hookWindow('__hf');
    hookWindow('__player');
    draw();
    return draw;
  }

  global.SceneKit = {
    version: '1.0.0',
    schema: SCHEMA,
    clamp: clamp,
    lerp: lerp,
    ease: ease,
    eases: Object.keys(EASES),
    progress: progress,
    fade: fade,
    rng: rng,
    scene: scene,
    frameOf: frameOf,
    phrase: phrase,
    phraseActive: phraseActive,
    cueAt: cueAt,
    safeRect: safeRect,
    breakLines: breakLines,
    captions: captions,
    attach: attach,
  };
})(typeof window !== 'undefined' ? window : this);
