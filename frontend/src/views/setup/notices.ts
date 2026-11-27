/**
 * Loads THIRD_PARTY_NOTICES, which internal/notices generates at the
 * repository root and vite.config.ts serves as this virtual module. The
 * import makes the page carry the file, so every build of the app ships it;
 * being dynamic, it is a chunk of its own, fetched only when someone asks to
 * read it.
 */
export const loadNotices = (): Promise<string> =>
  import("virtual:third-party-notices").then((notices) => notices.default);
