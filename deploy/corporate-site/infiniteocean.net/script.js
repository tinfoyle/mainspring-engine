const menuButton = document.querySelector(".menu-button");
const navigation = document.querySelector(".site-navigation");

if (menuButton && navigation) {
  menuButton.addEventListener("click", () => {
    const open = menuButton.getAttribute("aria-expanded") !== "true";
    menuButton.setAttribute("aria-expanded", String(open));
    menuButton.querySelector("span").textContent = open ? "Close" : "Menu";
    menuButton.querySelector("b").textContent = open ? "×" : "☰";
    navigation.classList.toggle("is-open", open);
  });

  navigation.addEventListener("click", (event) => {
    if (!(event.target instanceof HTMLAnchorElement)) return;
    menuButton.setAttribute("aria-expanded", "false");
    menuButton.querySelector("span").textContent = "Menu";
    menuButton.querySelector("b").textContent = "☰";
    navigation.classList.remove("is-open");
  });
}
