import { save } from "./save";

export function use(id: string): string {
  return save(id);
}
