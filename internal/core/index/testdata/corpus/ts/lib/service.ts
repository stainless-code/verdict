import { audit } from "./audit";

export class Service {
  @audit()
  run(): void {}

  @audit
  stop(): void {}
}
