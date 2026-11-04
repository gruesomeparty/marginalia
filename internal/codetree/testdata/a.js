import { x } from "y";

// Cap.
export const batchCap = 10;

export class Batch extends Base {
  cap = 0;
  constructor(c) { super(); }
  full() { return this.cap > 0; }
}

export function ingest(b) {}

export default function () {}

const handler = async (req) => 1;
