// Validates take files against spec/take.schema.json (JSON Schema 2020-12) with ajv.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { Ajv2020, type ErrorObject, type ValidateFunction } from 'ajv/dist/2020.js';
import { REPO } from './env.ts';

export const SCHEMA_PATH = join(REPO, 'spec', 'take.schema.json');
export const TAKE_FILES = ['session', 'segments', 'events', 'strokes', 'voice'] as const;
export type TakeFile = (typeof TAKE_FILES)[number];
export type SchemaDef = TakeFile | 'dataBody' | 'take';

let compiled: Map<SchemaDef, ValidateFunction> | null = null;

function validators(): Map<SchemaDef, ValidateFunction> {
  if (compiled) return compiled;
  const schema = JSON.parse(readFileSync(SCHEMA_PATH, 'utf8')) as { $id: string };
  const ajv = new Ajv2020({ allErrors: true, strict: true, strictRequired: false });
  ajv.addSchema(schema);
  compiled = new Map();
  for (const def of [...TAKE_FILES, 'dataBody'] as const) {
    compiled.set(def, ajv.getSchema(`${schema.$id}#/$defs/${def}`)!);
  }
  compiled.set('take', ajv.getSchema(schema.$id)!);
  return compiled;
}

const describe = (errors: ErrorObject[] | null | undefined): string =>
  (errors ?? []).map((e) => `${e.instancePath || '/'} ${e.message} ${JSON.stringify(e.params)}`).join('\n');

/** Returns the validation errors of `data` against one definition, or [] when it is valid. */
export function schemaErrors(def: SchemaDef, data: unknown): string[] {
  const v = validators().get(def)!;
  return v(data) ? [] : describe(v.errors).split('\n');
}

/** Throws with every error when `data` doesn't match the definition. */
export function assertValid(def: SchemaDef, data: unknown, what: string = def): void {
  const errors = schemaErrors(def, data);
  if (errors.length) throw new Error(`${what} does not match take.schema.json#/$defs/${def}:\n${errors.join('\n')}`);
}

/** Validates every JSON file of a take folder against its definition. Returns the parsed files. */
export function validateTakeDir(dir: string): Record<TakeFile, unknown> {
  const out = {} as Record<TakeFile, unknown>;
  for (const f of TAKE_FILES) {
    const data: unknown = JSON.parse(readFileSync(join(dir, `${f}.json`), 'utf8'));
    assertValid(f, data, `${dir}/${f}.json`);
    out[f] = data;
  }
  assertValid('take', out, dir);
  return out;
}
