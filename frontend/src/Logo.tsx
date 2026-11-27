/**
 * The Inkuna mark: an open inkuna holding a workflow that fans out from one
 * request into parallel stages and lands back together. The same drawing
 * backs build/appicon.svg, so the Dock and the sidebar agree.
 */
export function Logo({ size = 36, title }: { size?: number; title?: string }) {
  return (
    <svg
      viewBox="0 0 1024 1024"
      width={size}
      height={size}
      role={title ? "img" : undefined}
      aria-hidden={title ? undefined : true}
      aria-label={title}
    >
      <path
        d="M 226 480 A 290 290 0 0 0 798 480"
        fill="none"
        stroke="var(--color-accent)"
        strokeWidth="64"
        strokeLinecap="round"
      />
      <g
        stroke="var(--color-muted)"
        strokeWidth="22"
        strokeLinecap="round"
        fill="none"
      >
        <path d="M 512 318 L 392 462" />
        <path d="M 512 318 L 632 462" />
        <path d="M 392 462 L 512 606" />
        <path d="M 632 462 L 512 606" />
      </g>
      <circle cx="392" cy="462" r="44" fill="var(--color-ink)" />
      <circle cx="632" cy="462" r="44" fill="var(--color-ink)" />
      <circle cx="512" cy="606" r="44" fill="var(--color-ink)" />
      <circle cx="512" cy="318" r="58" fill="var(--color-accent)" />
    </svg>
  );
}
