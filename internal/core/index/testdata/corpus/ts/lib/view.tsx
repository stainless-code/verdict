import { save } from "./save";

export function View({ id }: { id: string }) {
  return <button onClick={() => save(id)}>save</button>;
}
