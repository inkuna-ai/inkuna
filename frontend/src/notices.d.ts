// vite.config.ts serves THIRD_PARTY_NOTICES as this module's text
// (views/setup/notices.ts).
declare module "virtual:third-party-notices" {
  const text: string;
  export default text;
}
