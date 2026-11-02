// 给合成页面里被点中的元素生成一个尽量短、又能唯一定位的 CSS 选择器，
// 写进消息让编排工具知道用户指的是哪个元素。

export function cssSelector(element: Element): string {
  const parts: string[] = [];
  let node: Element | null = element;
  while (node && node.nodeType === 1 && node.tagName.toLowerCase() !== 'html') {
    if (node.id) {
      parts.unshift('#' + CSS.escape(node.id));
      break;
    }
    let part = node.tagName.toLowerCase();
    const classes = [...node.classList].slice(0, 2);
    if (classes.length) part += classes.map((c) => '.' + CSS.escape(c)).join('');
    const parent: Element | null = node.parentElement;
    if (parent) {
      const same = [...parent.children].filter((child) => child.tagName === node!.tagName);
      if (same.length > 1) part += `:nth-of-type(${same.indexOf(node) + 1})`;
    }
    parts.unshift(part);
    if (part === 'body') break;
    node = parent;
  }
  return parts.join(' > ');
}
