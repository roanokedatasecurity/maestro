import {useEffect, useState} from 'react';
import './App.css';
import {GetStatus} from "../wailsjs/go/main/App";

function App() {
    const [status, setStatus] = useState('checking...');

    useEffect(() => {
        GetStatus().then(setStatus);
    }, []);

    return (
        <div id="App">
            <div id="result" className="result">{status}</div>
        </div>
    )
}

export default App
