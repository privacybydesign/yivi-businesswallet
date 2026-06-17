import { useEffect, useState } from "react";
import { getHealth } from "../api/client";

export default function Health() {
    const [status, setStatus] = useState("loading…");

    useEffect(() => {
        getHealth()
            .then((data) => setStatus(data.status))
            .catch(() => setStatus("error"));
    }, []);

    return <h1>Health: {status}</h1>;
}
