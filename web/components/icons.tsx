import type { SVGProps } from "react";

type P = SVGProps<SVGSVGElement> & { size?: number };

function Svg({ size = 16, children, ...rest }: P) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.5}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...rest}
    >
      {children}
    </svg>
  );
}

/** Brand mark: a rail switch — one track diverging into two. */
export function Mark({ size = 22, ...rest }: P) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" aria-hidden="true" {...rest}>
      <rect x="0.5" y="0.5" width="23" height="23" rx="6.5" fill="var(--signal)" />
      <path d="M6 19.5V13c0-2.2 1.8-4 4-4h0c2.8 0 4.5-1.6 4.5-4.5" stroke="var(--signal-ink)" strokeWidth="2" strokeLinecap="round" />
      <path d="M6 13V4.5" stroke="var(--signal-ink)" strokeWidth="2" strokeLinecap="round" />
      <circle cx="17.5" cy="17" r="2.2" fill="var(--signal-ink)" />
    </svg>
  );
}

export const IconOverview = (p: P) => (
  <Svg {...p}>
    <path d="M2 13.5h12" />
    <path d="M4 11V7.5M7 11V4M10 11V8.5M13 11V5.5" />
  </Svg>
);
export const IconRequests = (p: P) => (
  <Svg {...p}>
    <path d="M2.5 4h11M2.5 8h11M2.5 12h7" />
  </Svg>
);
export const IconLive = (p: P) => (
  <Svg {...p}>
    <circle cx="8" cy="8" r="1.6" fill="currentColor" stroke="none" />
    <path d="M5.2 10.8a4 4 0 0 1 0-5.6M10.8 5.2a4 4 0 0 1 0 5.6" />
    <path d="M3.1 12.9a7 7 0 0 1 0-9.8M12.9 3.1a7 7 0 0 1 0 9.8" />
  </Svg>
);
export const IconKey = (p: P) => (
  <Svg {...p}>
    <circle cx="5.5" cy="10.5" r="3" />
    <path d="M7.6 8.4 13.5 2.5M11.5 4.5l1.6 1.6M10 6l1.2 1.2" />
  </Svg>
);
export const IconTargets = (p: P) => (
  <Svg {...p}>
    <rect x="5" y="1.75" width="6" height="10" rx="3" />
    <circle cx="8" cy="4.75" r="1" fill="currentColor" stroke="none" />
    <circle cx="8" cy="8.75" r="1" fill="currentColor" stroke="none" />
    <path d="M8 11.75v2.5M5 14.25h6" />
  </Svg>
);
export const IconLogout = (p: P) => (
  <Svg {...p}>
    <path d="M6.5 2.5h-3a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h3M10.5 11l3-3-3-3M13.5 8H6" />
  </Svg>
);
export const IconRefresh = (p: P) => (
  <Svg {...p}>
    <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9M13.5 2.5v3h-3" />
  </Svg>
);
export const IconCopy = (p: P) => (
  <Svg {...p}>
    <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" />
    <path d="M10.5 5.5V3.5a1 1 0 0 0-1-1h-6a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2" />
  </Svg>
);
export const IconCheck = (p: P) => (
  <Svg {...p}>
    <path d="m3 8.5 3 3 7-7" />
  </Svg>
);
export const IconX = (p: P) => (
  <Svg {...p}>
    <path d="m4 4 8 8M12 4l-8 8" />
  </Svg>
);
export const IconPlus = (p: P) => (
  <Svg {...p}>
    <path d="M8 3v10M3 8h10" />
  </Svg>
);
export const IconPause = (p: P) => (
  <Svg {...p}>
    <path d="M5.5 3.5v9M10.5 3.5v9" />
  </Svg>
);
export const IconPlay = (p: P) => (
  <Svg {...p}>
    <path d="M5 3.2v9.6a.5.5 0 0 0 .77.42l7.2-4.8a.5.5 0 0 0 0-.84l-7.2-4.8A.5.5 0 0 0 5 3.2Z" />
  </Svg>
);
export const IconSun = (p: P) => (
  <Svg {...p}>
    <circle cx="8" cy="8" r="2.75" />
    <path d="M8 1.5v1.5M8 13v1.5M1.5 8H3M13 8h1.5M3.4 3.4l1 1M11.6 11.6l1 1M3.4 12.6l1-1M11.6 4.4l1-1" />
  </Svg>
);
export const IconMoon = (p: P) => (
  <Svg {...p}>
    <path d="M13.5 9.6A5.5 5.5 0 0 1 6.4 2.5a5.5 5.5 0 1 0 7.1 7.1Z" />
  </Svg>
);
export const IconAlert = (p: P) => (
  <Svg {...p}>
    <path d="M8 2 1.5 13.5h13L8 2Z" />
    <path d="M8 6.5v3M8 11.5v.01" />
  </Svg>
);
export const IconPlug = (p: P) => (
  <Svg {...p}>
    <path d="M5.5 1.5v3M10.5 1.5v3M3.5 4.5h9v2.5a4.5 4.5 0 0 1-9 0V4.5ZM8 11.5v3" />
  </Svg>
);
export const IconInbox = (p: P) => (
  <Svg {...p}>
    <path d="M1.5 9.5 3.5 3h9l2 6.5v3a1 1 0 0 1-1 1h-11a1 1 0 0 1-1-1v-3Z" />
    <path d="M1.5 9.5h3.5l1 1.5h4l1-1.5h3.5" />
  </Svg>
);
export const IconEdit = (p: P) => (
  <Svg {...p}>
    <path d="M10.5 2.5 13.5 5.5 5.5 13.5h-3v-3l8-8Z" />
  </Svg>
);
export const IconChevron = (p: P) => (
  <Svg {...p}>
    <path d="m6 3.5 4.5 4.5L6 12.5" />
  </Svg>
);
export const IconLock = (p: P) => (
  <Svg {...p}>
    <rect x="3" y="7" width="10" height="7" rx="1.5" />
    <path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" />
  </Svg>
);
export const IconMenu = (p: P) => (
  <Svg {...p}>
    <path d="M2.5 5h11M2.5 11h11" />
  </Svg>
);
