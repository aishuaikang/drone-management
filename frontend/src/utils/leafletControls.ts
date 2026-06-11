import L from "leaflet";

export interface CustomDrawButtonOptions {
  title: string;
  text: string;
  className?: string;
  contentType?: "text" | "image" | "html";
  imageOptions?: {
    width?: string;
    height?: string;
    alt?: string;
  };
  onClick: (event: Event, map: L.Map) => void;
}

export function createDrawControlButtonGroup(
  buttons: CustomDrawButtonOptions[],
  position: L.ControlPosition = "topleft",
): L.Control {
  const CustomButtonGroup = L.Control.extend({
    options: { position },

    onAdd(map: L.Map) {
      const container = L.DomUtil.create("div", "leaflet-bar leaflet-control leaflet-control-custom-group");

      buttons.forEach((buttonOptions, index) => {
        const button = L.DomUtil.create("a", buttonOptions.className || "", container);
        button.href = "#";
        button.title = buttonOptions.title;
        button.setAttribute("role", "button");
        button.setAttribute("aria-label", buttonOptions.title);

        switch (buttonOptions.contentType || "text") {
          case "image": {
            const img = document.createElement("img");
            img.src = buttonOptions.text;
            img.alt = buttonOptions.imageOptions?.alt || buttonOptions.title;
            img.style.width = buttonOptions.imageOptions?.width || "20px";
            img.style.height = buttonOptions.imageOptions?.height || "20px";
            img.style.objectFit = "contain";
            img.style.pointerEvents = "none";
            button.appendChild(img);
            break;
          }
          case "html":
            button.innerHTML = buttonOptions.text;
            break;
          default:
            button.textContent = buttonOptions.text;
            break;
        }

        button.style.backgroundColor = "white";
        button.style.backgroundRepeat = "no-repeat";
        button.style.backgroundPosition = "center";
        button.style.border = "none";
        button.style.borderRadius = index === 0 ? "4px 4px 0 0" : index === buttons.length - 1 ? "0 0 4px 4px" : "0";
        button.style.borderBottom = index === buttons.length - 1 ? "none" : "1px solid #ccc";
        button.style.width = "30px";
        button.style.height = "30px";
        button.style.lineHeight = "30px";
        button.style.textAlign = "center";
        button.style.textDecoration = "none";
        button.style.color = "#333";
        button.style.cursor = "pointer";
        button.style.display = "block";

        L.DomEvent.on(button, "click", (event: Event) => {
          L.DomEvent.stopPropagation(event);
          L.DomEvent.preventDefault(event);
          buttonOptions.onClick(event, map);
        });
      });

      L.DomEvent.disableClickPropagation(container);
      return container;
    },

    onRemove() {
      // Leaflet removes the control container; no external listeners are kept.
    },
  });

  return new CustomButtonGroup();
}
