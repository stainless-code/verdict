import { save } from "../lib/save";

export function Screen({ id }: { id: string }) {
  return <div onClick={() => save(id)}>screen</div>;
}
