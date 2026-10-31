import { x } from "y";

// Cap.
export const batchCap = 10, other = 2;
let z = 1;

export interface Doer {
  do(): void;
  name: string;
}

export type Id = string;

enum Color { Red, Green }

export class Batch extends Base implements Doer {
  private cap = 0;
  constructor(c: number) { super(); }
  full(): boolean { return this.cap > 0; }
  get size() { return 1; }
}

export function ingest(b: Batch): void {}

export default function () {}

const handler = async (req) => { return 1; };

namespace NS { export function f() {} }
