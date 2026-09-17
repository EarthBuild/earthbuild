import { accessSync, constants } from "node:fs";
import { delimiter, isAbsolute, join } from "node:path";

import * as vscode from "vscode";
import {
  LanguageClient,
  type LanguageClientOptions,
  type ServerOptions,
} from "vscode-languageclient/node";

const CLIENT_ID = "earthbuild";
const CLIENT_NAME = "EarthBuild Language Server";
const LANGUAGE_ID = "earth";
const BINARY = "earth";

let client: LanguageClient | undefined;

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  context.subscriptions.push(
    vscode.commands.registerCommand(`${CLIENT_ID}.restartLanguageServer`, async () => {
      await stop();
      await start();
    }),
  );

  await start();
}

export async function deactivate(): Promise<void> {
  await stop();
}

async function start(): Promise<void> {
  const settings = vscode.workspace.getConfiguration(CLIENT_ID);
  const configured = settings.get<string>("lsp.path", "").trim();

  const command = configured === "" ? findOnPath(BINARY) : configured;
  if (command === undefined) {
    await reportMissingBinary();
    return;
  }

  const serverOptions: ServerOptions = {
    command,
    args: settings.get<string[]>("lsp.arguments", ["lsp"]),
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: "file", language: LANGUAGE_ID }],
    outputChannel: vscode.window.createOutputChannel(CLIENT_NAME),
  };

  client = new LanguageClient(CLIENT_ID, CLIENT_NAME, serverOptions, clientOptions);

  try {
    await client.start();
  } catch (error) {
    client = undefined;
    await vscode.window.showErrorMessage(
      `Could not start ${CLIENT_NAME} using "${command}": ${describe(error)}`,
    );
  }
}

async function stop(): Promise<void> {
  const running = client;
  client = undefined;

  if (running !== undefined) {
    await running.stop();
  }
}

/**
 * Looks the binary up on the PATH this process inherited.
 *
 * VS Code launched from Finder or the Dock does not inherit a login shell's
 * PATH, so `earth` can be missing here while it works in the user's terminal.
 * Resolving it ourselves lets us name the setting that fixes that instead of
 * failing with a bare spawn error.
 */
function findOnPath(binary: string): string | undefined {
  const names =
    process.platform === "win32"
      ? (process.env.PATHEXT ?? ".EXE").split(delimiter).map((ext) => binary + ext.toLowerCase())
      : [binary];

  for (const directory of (process.env.PATH ?? "").split(delimiter)) {
    if (directory === "") {
      continue;
    }

    for (const name of names) {
      const candidate = isAbsolute(directory) ? join(directory, name) : undefined;
      if (candidate === undefined) {
        continue;
      }

      try {
        accessSync(candidate, constants.X_OK);
        return candidate;
      } catch {
        // Not executable here; keep looking.
      }
    }
  }

  return undefined;
}

async function reportMissingBinary(): Promise<void> {
  const openSettings = "Open Settings";

  const choice = await vscode.window.showWarningMessage(
    `${BINARY} was not found on the PATH that VS Code inherited. Install EarthBuild, ` +
      `or set "${CLIENT_ID}.lsp.path" to its absolute path. On macOS an app launched from ` +
      `Finder does not inherit a login shell's PATH, so this can happen even when ` +
      `"${BINARY} --version" works in a terminal.`,
    openSettings,
  );

  if (choice === openSettings) {
    await vscode.commands.executeCommand("workbench.action.openSettings", `${CLIENT_ID}.lsp.path`);
  }
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
