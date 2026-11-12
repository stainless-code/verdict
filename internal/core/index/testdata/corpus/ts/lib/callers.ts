import { save } from "./save";

export const handlers = { save };

export function flush(id: string): void {
  save(id);
  const text = `saved ${save(id)}`;
  void text;
  handlers.save(id);
}
