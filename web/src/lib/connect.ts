// SPDX-License-Identifier: AGPL-3.0-only
// The connection snippets of Settings and the example prompts. Pure text
// builders so the install commands stay testable.

/** MASK stands in for the token while it is hidden. */
export const MASK = '••••••••••••••••';

export interface SnippetInput {
  /** The MCP address, e.g. https://folio.example.com/mcp. */
  mcpUrl: string;
  /** The bearer token (or MASK); null when this Folio needs none. */
  token: string | null;
}

export interface Snippets {
  codex: string;
  cursor: string;
  gemini: string;
}

/** Configuration for the agents that take a URL and a header. */
export function snippets({ mcpUrl, token }: SnippetInput): Snippets {
  const auth = token === null ? null : `Bearer ${token}`;
  const codex = `[mcp_servers.folio]\nurl = "${mcpUrl}"` + (auth ? `\nhttp_headers = { Authorization = "${auth}" }` : '');
  const cursor = JSON.stringify({ mcpServers: { folio: { url: mcpUrl, ...(auth ? { headers: { Authorization: auth } } : {}) } } }, null, 2);
  const gemini = JSON.stringify({ mcpServers: { folio: { httpUrl: mcpUrl, ...(auth ? { headers: { Authorization: auth } } : {}) } } }, null, 2);
  return { codex, cursor, gemini };
}

/** Claude Code installs Folio through its plugin: MCP connection and skill together. */
export const claudePlugin = {
  /** The in-session equivalents; the plugin then prompts for the address and token. */
  inSession: '/plugin marketplace add vocdoni/folio\n/plugin install folio@folio',
};

/**
 * One command: adds the marketplace if missing, installs for the user and stores the
 * address (the bare base URL; the plugin appends /mcp) and the token (kept with Claude Code's credentials).
 */
export function claudeInstall(base: string, token: string | null): string {
  return (
    'claude plugin install folio --marketplace vocdoni/folio --scope user \\\n' +
    `  --config folio_url=${base}` +
    (token === null ? '' : ` \\\n  --config folio_token='${token}'`)
  );
}

/** pi coding agent: user-level MCP registration through an environment variable, plus its skill. */
export function piSetup(base: string, token: string | null): { commands: string; json: string } {
  const header = token === null ? '' : ' --bearer-token-env-var FOLIO_TOKEN';
  const commands = [
    ...(token === null ? [] : ['# 1. Make the token available to pi (add to your shell profile)', `export FOLIO_TOKEN='${token}'`]),
    `# ${token === null ? '1' : '2'}. Register Folio for every project (user level, written to ~/.pi/agent/mcp.json)`,
    `pi mcp add folio --url ${base}/mcp${header}`,
    'pi mcp list            # should show folio connected with its tools',
    `# ${token === null ? '2' : '3'}. Install the Folio skill`,
    skillInstall(base, '~/.pi/agent/skills'),
  ].join('\n');
  const folio: Record<string, unknown> = { url: `${base}/mcp` };
  if (token !== null) folio.headers = { Authorization: 'Bearer ${FOLIO_TOKEN}' };
  return { commands, json: JSON.stringify({ mcpServers: { folio } }, null, 2) };
}

/** The command that saves the skill into an agent's user-level skills folder. */
export function skillInstall(base: string, skillsDir: string): string {
  return `mkdir -p ${skillsDir}/folio && curl -fsSL ${base}/skills/folio/SKILL.md -o ${skillsDir}/folio/SKILL.md`;
}

export interface ExamplePrompt {
  title: string;
  text: string;
}

export const examplePrompts: ExamplePrompt[] = [
  {
    title: 'A proposal from your notes',
    text: 'Use Folio to turn these meeting notes into a project proposal for ACME Corp using the proposal template. Keep my wording and the figures exactly as written.',
  },
  {
    title: 'A quote with exact figures',
    text: 'Make a quote for Globex with these line items and prices, using the figures exactly as I wrote them, and give me the PDF and DOCX links.',
  },
  {
    title: 'Revise and publish',
    text: 'Revise the ACME proposal: change the delivery date to 15 December and publish the new revision.',
  },
  {
    title: 'Find your recent documents',
    text: 'Show my last five Folio documents with their download links.',
  },
  {
    title: 'A template from a description',
    text: 'Create a Folio template for meeting minutes with attendees, decisions and action items, then show me a preview.',
  },
  {
    title: 'A brand for your company',
    text: 'Create a Folio brand for Initech with primary colour #0B5D4B, accent colour #F2A900 and this logo, then render a one-page letter with it so I can check the look.',
  },
  {
    title: 'A report from a Markdown file',
    text: 'Turn this Markdown file into a technical report with Folio and send me the PDF and DOCX links. Do not change any wording.',
  },
  {
    title: 'A statement of work with milestones',
    text: 'Write a statement of work for the Hooli website redesign with three milestones: design by 30 November, build by 31 January and launch on 28 February. Use the amounts I give you exactly as written.',
  },
  {
    title: 'Your words, untouched',
    text: 'Format this text as a letter with Folio without changing a single word, typos included. Tell me if the template needs a detail I did not give you.',
  },
];
