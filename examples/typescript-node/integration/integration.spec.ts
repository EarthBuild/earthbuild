import axios from "axios";
import app from "../src/index";
import { Server } from "http";
import * as net from "net";

describe("sayHello", () => {
  let server: Server | undefined;

  const isPort8080Open = (): Promise<boolean> => {
    return new Promise((resolve) => {
      const socket = new net.Socket();
      const onError = () => {
        socket.destroy();
        resolve(false);
      };
      socket.setTimeout(1000);
      socket.once("error", onError);
      socket.once("timeout", onError);
      socket.connect(8080, "localhost", () => {
        socket.end();
        resolve(true);
      });
    });
  };

  beforeAll(async () => {
    const open = await isPort8080Open();
    if (open) {
      console.log("Server already running on port 8080, skipping local startup");
      return;
    }

    return new Promise<void>((resolve, reject) => {
      server = app.listen(8080, () => {
        resolve();
      });
      server.on("error", (err: any) => {
        if (err.code === "EADDRINUSE") {
          server = undefined;
          resolve();
        } else {
          reject(err);
        }
      });
    });
  });

  afterAll((done) => {
    if (server) {
      server.close(done);
    } else {
      done();
    }
  });

  const call = async (who?: string) => {
    const url = who
      ? `http://localhost:8080/hello?who=${who}`
      : "http://localhost:8080/hello";
    const response = await axios.get(url);
    expect(response.status).toBe(200);
    return response.data;
  };

  it("should say Hello Earthly if nothing is passed", async () => {
    expect(await call()).toBe("Hello Earthly");
  });

  it("should say Hello World if World is passed", async () => {
    expect(await call("World")).toBe("Hello World");
  });
});
