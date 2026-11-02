// 线条图标，跟随文字颜色。只收界面里真正用到的几个。
const paths = {
  chevron: <path d="M3 4.5l3 3 3-3" />,
  prev: <path d="M7.5 2.5L4 6l3.5 3.5" />,
  next: <path d="M4.5 2.5L8 6 4.5 9.5" />,
  play: <path d="M3.5 2l6.5 4-6.5 4z" fill="currentColor" stroke="none" />,
  pause: <path d="M3.5 2.5v7M8.5 2.5v7" stroke-width="2" />,
  stop: <rect x="2.5" y="2.5" width="7" height="7" rx="1.2" fill="currentColor" stroke="none" />,
  send: <path d="M6 10V2.5M2.5 6L6 2.5 9.5 6" />,
  close: <path d="M3 3l6 6M9 3L3 9" />,
  plus: <path d="M6 2v8M2 6h8" />,
  region: <rect x="1.5" y="1.5" width="9" height="9" rx="1" stroke-dasharray="2 1.6" />,
  pointer: <path d="M2 1.5l3.4 8.5 1.3-3.4 3.4-1.3z" />,
  up: <path d="M6 9.5V2.5M2.5 6L6 2.5 9.5 6" />,
  folder: <path d="M1.5 3.5h3l1 1.2h5v5.3h-9z" />,
  film: (
    <>
      <rect x="1.5" y="2" width="9" height="8" rx="1" />
      <path d="M1.5 4.5h9M1.5 7.5h9M4 2v8" />
    </>
  ),
} as const;

export type IconName = keyof typeof paths;

export function Icon({ name, size = 12 }: { name: IconName; size?: number }) {
  return (
    <svg
      aria-hidden="true"
      width={size}
      height={size}
      viewBox="0 0 12 12"
      fill="none"
      stroke="currentColor"
      stroke-width="1.4"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      {paths[name]}
    </svg>
  );
}
