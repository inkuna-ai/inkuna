import {
  useEffect,
  useMemo,
  useRef,
  type ReactNode,
  type RefObject,
} from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { openExternal } from "../../bridge";
import { MermaidDiagram } from "./Mermaid";
import { rehypeHeadingIDs } from "./anchors";
import { resolveLink } from "./tree";

const remarkPlugins = [remarkGfm];
const rehypePlugins = [rehypeHeadingIDs];

const linkClass =
  "text-accent underline decoration-accent-line underline-offset-2 hover:decoration-accent";
const headingClass = "font-semibold tracking-[-0.01em] text-ink-strong";

interface LinkHandlers {
  onOpenDoc(path: string, fragment: string): void;
  onAnchor(fragment: string): void;
}

/**
 * Renders a doc's Markdown: GitHub-flavoured, with raw HTML dropped and
 * images left unloaded, so a doc cannot inject markup or reach the network.
 * Links to other docs open in the view; web links open in the browser.
 */
export function Markdown({
  markdown,
  path,
  isDoc,
  onOpenDoc,
  onAnchor,
}: {
  markdown: string;
  /** The doc's path under the docs root; relative links resolve from it. */
  path: string;
  /** Whether a path is a doc in the tree; keep it stable between renders. */
  isDoc(path: string): boolean;
} & LinkHandlers) {
  // The view re-renders for unrelated reasons, such as a filter or a folder
  // toggle. New components would make React remount the whole doc and redraw
  // every diagram, so they change only with what decides the markup; the
  // click handlers are read when a link is followed.
  const handlers = useRef<LinkHandlers>({ onOpenDoc, onAnchor });
  useEffect(() => {
    handlers.current = { onOpenDoc, onAnchor };
  });
  const components = useMemo(
    () => markdownComponents(path, isDoc, handlers),
    [path, isDoc],
  );
  // Parsing runs on every render of ReactMarkdown, so the rendered doc is
  // kept until its text or components change.
  const rendered = useMemo(
    () => (
      <ReactMarkdown
        remarkPlugins={remarkPlugins}
        rehypePlugins={rehypePlugins}
        skipHtml
        components={components}
      >
        {markdown}
      </ReactMarkdown>
    ),
    [markdown, components],
  );
  return (
    <div className="min-w-0 text-sm text-ink wrap-anywhere">{rendered}</div>
  );
}

function markdownComponents(
  path: string,
  isDoc: (path: string) => boolean,
  handlers: RefObject<LinkHandlers>,
): Components {
  // Ids come from rehypeHeadingIDs or GFM; classes such as the footnote
  // label's sr-only are kept.
  const heading =
    (Tag: "h1" | "h2" | "h3" | "h4" | "h5" | "h6", style: string) =>
    ({
      children,
      id,
      className,
    }: {
      children?: ReactNode;
      id?: string;
      className?: string;
    }) => (
      <Tag id={id} className={`${headingClass} ${style} ${className ?? ""}`}>
        {children}
      </Tag>
    );

  return {
    h1: heading("h1", "mt-8 mb-3 text-2xl first:mt-0"),
    h2: heading("h2", "mt-8 mb-3 border-b border-line pb-1.5 text-xl"),
    h3: heading("h3", "mt-6 mb-2 text-base"),
    h4: heading("h4", "mt-5 mb-2 text-sm"),
    h5: heading("h5", "mt-4 mb-2 text-sm"),
    h6: heading("h6", "mt-4 mb-2 text-xs text-muted"),
    p: ({ children }) => <p className="my-3 leading-relaxed">{children}</p>,
    ul: ({ children, className }) => (
      <ul
        className={`my-3 pl-6 ${className?.includes("contains-task-list") ? "list-none pl-1" : "list-disc"}`}
      >
        {children}
      </ul>
    ),
    ol: ({ children, start }) => (
      <ol start={start} className="my-3 list-decimal pl-6">
        {children}
      </ol>
    ),
    // A footnote is a list item GFM gives an id that its reference links to.
    li: ({ children, id }) => (
      <li id={id} className="my-1 leading-relaxed">
        {children}
      </li>
    ),
    section: ({ children, className }) => (
      <section
        className={
          className?.includes("footnotes")
            ? "mt-8 border-t border-line pt-3 text-xs text-muted"
            : undefined
        }
      >
        {children}
      </section>
    ),
    input: ({ checked, type }) =>
      type === "checkbox" ? (
        <input
          type="checkbox"
          checked={checked}
          disabled
          className="mr-2 align-middle"
        />
      ) : null,
    blockquote: ({ children }) => (
      <blockquote className="my-4 border-l-2 border-line-strong pl-4 text-muted">
        {children}
      </blockquote>
    ),
    hr: () => <hr className="my-6 border-0 border-t border-line" />,
    table: ({ children }) => (
      <div className="my-4 overflow-x-auto">
        <table className="w-full border-collapse text-left text-xs">
          {children}
        </table>
      </div>
    ),
    th: ({ children, style }) => (
      <th
        style={style}
        className="border border-line bg-raised px-3 py-2 font-semibold"
      >
        {children}
      </th>
    ),
    td: ({ children, style }) => (
      <td style={style} className="border border-line px-3 py-2 align-top">
        {children}
      </td>
    ),
    code: ({ children, className }) => (
      <code
        className={`rounded-sm bg-raised px-1 py-0.5 font-mono text-[0.85em] ${className ?? ""}`}
      >
        {children}
      </code>
    ),
    pre: ({ node, children }) => {
      const code = node?.children[0];
      if (code?.type === "element" && code.tagName === "code") {
        const classes = code.properties.className;
        if (Array.isArray(classes) && classes.includes("language-mermaid")) {
          const source = code.children
            .map((child) => (child.type === "text" ? child.value : ""))
            .join("");
          return <MermaidDiagram source={source} />;
        }
      }
      return (
        <pre className="my-4 overflow-x-auto border border-line bg-field p-4 font-mono text-xs leading-relaxed [&>code]:bg-transparent [&>code]:p-0">
          {children}
        </pre>
      );
    },
    img: ({ alt }) => (
      <span
        title="Images are not loaded in the Docs view"
        className="inline-block border border-dashed border-line-strong px-2 py-0.5 text-[11px] text-muted"
      >
        Image{alt ? `: ${alt}` : ""}
      </span>
    ),
    a: ({
      href = "",
      children,
      id,
      "aria-label": label,
      "aria-describedby": describedBy,
    }) => {
      const link = resolveLink(path, href);
      // GFM's footnote references and back-links carry these; a reference's
      // id is where its back-link returns to.
      const generated = {
        id,
        "aria-label": label,
        "aria-describedby": describedBy,
      };
      // The webview must stay on the app: every link is handled here, and
      // a middle click must not open the target in a new window either.
      const follow =
        (action: () => void) =>
        (event: React.MouseEvent<HTMLAnchorElement>) => {
          event.preventDefault();
          if (event.type === "click") action();
        };
      if (link.kind === "external") {
        return (
          <a
            {...generated}
            href={link.url}
            title={`Opens ${link.url} in your browser`}
            className={linkClass}
            onClick={follow(() => openExternal(link.url))}
            onAuxClick={follow(() => {})}
          >
            {children}
            <span aria-hidden="true"> ↗</span>
          </a>
        );
      }
      if (link.kind === "anchor" || (link.kind === "doc" && isDoc(link.path))) {
        return (
          <a
            {...generated}
            href={href}
            className={linkClass}
            onClick={follow(() =>
              link.kind === "anchor"
                ? handlers.current.onAnchor(link.fragment)
                : handlers.current.onOpenDoc(link.path, link.fragment),
            )}
            onAuxClick={follow(() => {})}
          >
            {children}
          </a>
        );
      }
      return (
        <span
          id={id}
          title={
            link.kind === "doc"
              ? `${link.path} is not in this project's docs`
              : `${href} is outside the docs`
          }
          className="text-ink-soft underline decoration-line-strong decoration-dotted underline-offset-2"
        >
          {children}
        </span>
      );
    },
  };
}
