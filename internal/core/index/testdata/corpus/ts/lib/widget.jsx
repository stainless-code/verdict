import { save } from "./save";

export function Widget({ id }) {
  save(id);
  return <span className="widget">{id}</span>;
}
